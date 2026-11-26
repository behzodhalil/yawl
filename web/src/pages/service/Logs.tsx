import { useContext, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { ApiError, api } from "../../api";
import { LogLines, isFailureLine, stamp, stampFormat, zoneName } from "../../LogView";
import { LogAlerts } from "./LogAlerts";
import { Deploys, firstLine } from "./Deploy";
import { bytes, count, ms, shortRef } from "../../format";
import { Button, InputGroup, LayerCard, Select, Table, Tabs, Toolbar, cn } from "@cloudflare/kumo";
import { Callout, Icon, Loading, Skeleton, errText, notify, useLoad } from "../../ui";

type Line = { Time: string; Deploy: string; Text: string; Stream?: "stderr" };

const UTC_KEY = "yawl.logs.utc";
const PRESSED = "aria-pressed:bg-kumo-tint aria-pressed:text-kumo-default aria-pressed:font-semibold";

type Req = {
  id: string; at: string; method: string; path: string; status: number; ms: number; bytes: number; by: string;
  host: string; ip: string; ua: string; req_bytes: number; type: string; replica: string; deploy: string; upstream_ms: number;
  logs: { Time: string; Text: string }[];
};
type Requests = { count: number; s5: number; p50: number; p95: number; slowest: string; rows: Req[] };
type Props = { project: string; env: string; service: string };

const LIVE_MS = 2000;
const SLOW_MS = 1000;
const ANY = "any";
const LABEL = "max-sm:sr-only";

type Zone = { utc: boolean; onUtc: () => void };

function ZoneButton({ utc, onUtc }: Zone) {
  return <Toolbar.Button aria-label={`Times in ${zoneName(utc)}`} title={utc ? "Show local times" : "Show UTC times"} onClick={onUtc}>{zoneName(utc)}</Toolbar.Button>;
}

export function Logs(props: Props) {
  const base = `/api/services/${props.project}/${props.env}/${props.service}`;
  const [kept, setKept] = useState(false);
  useEffect(() => {
    let live = true;
    api("GET", `${base}/requests`).then(() => live && setKept(true), (e) => live && setKept(!(e instanceof ApiError && e.status === 404)));
    return () => { live = false; };
  }, [base]);
  const [view, setView] = useState("app");
  const [find, setFind] = useState("");
  const [utc, setUtc] = useState(() => { try { return localStorage.getItem(UTC_KEY) === "1"; } catch { return false; } });
  const onUtc = () => {
    setUtc(!utc);
    try { localStorage.setItem(UTC_KEY, utc ? "0" : "1"); } catch { return; }
  };
  return (
    <div className="flex flex-col gap-3">
      {kept && (
        <div role="group" aria-label="Log kind" className="self-start">
          <Tabs variant="segmented" size="sm" tabs={[{ value: "app", label: "App logs" }, { value: "requests", label: "Requests" }]} value={view} onValueChange={setView} />
        </div>
      )}
      {view === "app" ? <AppLogs key={find} {...props} search={find} utc={utc} onUtc={onUtc} />
        : <RequestLog path={`${base}/requests`} utc={utc} onUtc={onUtc} onOpen={(id) => { setFind(id); setView("app"); }} />}
    </div>
  );
}

const quote = (s: string) => `'${s.replaceAll("'", `'\\''`)}'`;
const curl = (r: Req) => `curl -X ${quote(r.method)} ${quote(`https://${r.host}${r.path}`)} -H ${quote(`user-agent: ${r.ua}`)}`;
const statusTone = (n: number) => (n >= 500 ? "text-kumo-danger" : n >= 400 ? "text-kumo-warning" : "");

function Pairs({ title, rows }: { title: string; rows: [string, string][] }) {
  return (
    <div className="flex min-w-0 flex-col gap-1">
      <b className="text-xs font-medium text-kumo-subtle">{title}</b>
      <dl className="m-0 font-mono text-xs">
        {rows.map(([k, v]) => <div key={k} className="flex gap-2"><dt className="shrink-0 text-kumo-subtle">{k}:</dt><dd className="m-0 break-all">{v}</dd></div>)}
      </dl>
    </div>
  );
}

function RequestDetail({ r, onOpen }: { r: Req; onOpen: (id: string) => void }) {
  return (
    <div className="flex flex-col gap-3 py-2">
      <div className="grid gap-4 sm:grid-cols-2">
        <Pairs title="Request" rows={[["host", r.host], ["x-request-id", r.id], ["ip", r.ip], ["user-agent", r.ua], ["content-length", String(r.req_bytes)]]} />
        <Pairs title="Response" rows={[["content-type", r.type], ["replica", r.replica], ["deploy", r.deploy], ["upstream time", ms(r.upstream_ms)]]} />
      </div>
      <div className="flex flex-col gap-1">
        <b className="text-xs font-medium text-kumo-subtle">App log with this request id</b>
        <div className="logs">{r.logs.length === 0 ? <div className="text-kumo-subtle">No lines carry this id.</div> : <LogLines rows={r.logs.map((l) => ({ time: l.Time, text: l.Text }))} />}</div>
      </div>
      <div className="flex flex-wrap gap-2">
        <Button size="sm" variant="secondary" icon={<Icon name="logs" />} onClick={() => onOpen(r.id)}>Open in app logs</Button>
        <Button size="sm" variant="secondary" icon={<Icon name="copy" />} onClick={() => navigator.clipboard.writeText(curl(r)).then(() => notify("Copied as curl"), () => notify("Could not copy", "error"))}>Copy as curl</Button>
      </div>
    </div>
  );
}

function RequestLog({ path, onOpen, utc, onUtc }: { path: string; onOpen: (id: string) => void } & Zone) {
  const [status, setStatus] = useState(ANY);
  const [method, setMethod] = useState(ANY);
  const [who, setWho] = useState(ANY);
  const [slow, setSlow] = useState(false);
  const [live, setLive] = useState(false);
  const [open, setOpen] = useState("");
  const params = new URLSearchParams();
  if (status !== ANY) params.set("status", status);
  if (method !== ANY) params.set("method", method);
  if (who !== ANY) params.set("who", who);
  if (slow) params.set("slower", String(SLOW_MS));
  const data = useLoad<Requests>(`${path}?${params}`, live ? LIVE_MS : undefined);
  const d = data.data;
  const fmt = useMemo(() => stampFormat(utc), [utc]);
  const select = "w-auto rounded-none ring-0";
  const tiles: [string, string][] = d ? [["Requests · last hour", count(d.count)], ["5xx", `${d.s5}%`], ["p50", ms(d.p50)], ["p95", ms(d.p95)], ["Slowest path", d.slowest]] : [];
  return (
    <div className="flex flex-col gap-3">
      {d && (
        <div className="flex flex-wrap items-center gap-2.5">
          {tiles.map(([label, value]) => (
            <LayerCard key={label} render={<dl />} className="m-0 min-w-35 px-4 py-3">
              <dt className="text-xs text-kumo-subtle">{label}</dt>
              <dd className="m-0 font-mono text-[22px]">{value}</dd>
            </LayerCard>
          ))}
        </div>
      )}
      <div className="logview">
        <Toolbar className="logbar w-full flex-wrap rounded-none shadow-none ring-0" aria-label="Request filters">
          <Select aria-label="Status" className={select} value={status} onValueChange={(v) => setStatus(String(v))}
            items={{ [ANY]: "Any status", "2xx": "2xx", "3xx": "3xx", "4xx": "4xx", "5xx": "5xx" }} />
          <Select aria-label="Method" className={select} value={method} onValueChange={(v) => setMethod(String(v))}
            items={{ [ANY]: "Any method", GET: "GET", POST: "POST", PUT: "PUT", PATCH: "PATCH", DELETE: "DELETE" }} />
          <Select aria-label="Sent by" className={select} value={who} onValueChange={(v) => setWho(String(v))}
            items={{ [ANY]: "Humans and bots", human: "Humans", bot: "Search and other bots", ai: "AI crawlers" }} />
          <Toolbar.Button className={PRESSED} aria-pressed={slow} onClick={() => setSlow(!slow)}>Slower than 1s</Toolbar.Button>
          <Toolbar.Button className={PRESSED} icon={<Icon name="flash" />} aria-pressed={live} onClick={() => setLive(!live)}>Live</Toolbar.Button>
          <ZoneButton utc={utc} onUtc={onUtc} />
        </Toolbar>
        {!d ? <Loading what={data} /> : (
          <div className="overflow-x-auto">
            <Table layout="fixed" className="min-w-[720px]">
              <colgroup><col className="w-36" /><col /><col className="w-20" /><col className="w-28" /><col className="w-24" /><col className="w-40" /></colgroup>
              <Table.Header><Table.Row><Table.Head>Time</Table.Head><Table.Head>Request</Table.Head><Table.Head>Status</Table.Head><Table.Head>Time taken</Table.Head><Table.Head>Size</Table.Head><Table.Head>Sent by</Table.Head></Table.Row></Table.Header>
              <Table.Body>
                {d.rows.length === 0 && <Table.Row><Table.Cell colSpan={6} className="text-kumo-subtle">No requests match.</Table.Cell></Table.Row>}
                {d.rows.flatMap((r) => [
                  <Table.Row key={r.id}>
                    <Table.Cell className="font-mono text-xs text-kumo-subtle tabular-nums">{stamp(fmt, r.at)}</Table.Cell>
                    <Table.Cell>
                      <button type="button" aria-expanded={open === r.id} className="flex items-center gap-2 text-left font-mono text-sm" onClick={() => setOpen(open === r.id ? "" : r.id)}>
                        <Icon name={open === r.id ? "down" : "chev"} /><span className="font-semibold">{r.method}</span><span className="break-all">{r.path}</span>
                      </button>
                    </Table.Cell>
                    <Table.Cell className={cn("font-mono text-sm tabular-nums", statusTone(r.status))}>{r.status}</Table.Cell>
                    <Table.Cell className={cn("font-mono text-sm tabular-nums", r.ms >= SLOW_MS && "text-kumo-warning")}>{ms(r.ms)}</Table.Cell>
                    <Table.Cell className="font-mono text-sm tabular-nums">{bytes(r.bytes)}</Table.Cell>
                    <Table.Cell className="text-sm">{r.by}</Table.Cell>
                  </Table.Row>,
                  ...(open === r.id ? [<Table.Row key={r.id + "-detail"}><Table.Cell colSpan={6}><RequestDetail r={r} onOpen={onOpen} /></Table.Cell></Table.Row>] : []),
                ])}
              </Table.Body>
            </Table>
          </div>
        )}
      </div>
      <span className="text-sm text-kumo-subtle">Kept 7 days on the server. Query strings are kept, cookies and the Authorization header never are.</span>
    </div>
  );
}

function AppLogs({ project, env, service, search, utc, onUtc }: Props & Zone & { search: string }) {
  const path = `/api/services/${project}/${env}/${service}/logs`;
  const [lines, setLines] = useState<Line[]>([]);
  const [q, setQ] = useState(search);
  const [query, setQuery] = useState(search);
  const [follow, setFollow] = useState(false);
  const [errors, setErrors] = useState(false);
  const [err, setErr] = useState("");
  const [loaded, setLoaded] = useState(false);
  const [drain, setDrain] = useState(false);
  const [hours, setHours] = useState(0);
  const [full, setFull] = useState(false);
  const last = useRef("");
  const body = useRef<HTMLDivElement>(null);
  const pinned = useRef(true);
  const deploys = useContext(Deploys).data;
  const subjects = useMemo(() => Object.fromEntries((deploys ?? []).map((d) => [d.id, d.subject ? firstLine(d.subject) : shortRef(d)])), [deploys]);

  useEffect(() => {
    const t = setTimeout(() => setQuery(q), 300);
    return () => clearTimeout(t);
  }, [q]);

  useEffect(() => {
    let live = true;
    const params = new URLSearchParams({ n: hours || errors ? "1000" : "200" });
    if (query) params.set("q", query);
    if (hours) {
      params.set("from", "drain");
      params.set("since", new Date(Date.now() - hours * 3600e3).toISOString());
    }
    api<{ Lines: Line[]; Drain?: boolean }>("GET", `${path}?${params}`).then(
      (d) => {
        if (!live) return;
        setDrain((x) => x || !!d.Drain);
        setLines(d.Lines ?? []);
        last.current = d.Lines?.length ? d.Lines[d.Lines.length - 1].Time : "";
        setErr("");
        setLoaded(true);
      },
      (e) => live && (setErr(errText(e)), setLoaded(true)),
    );
    return () => { live = false; };
  }, [path, query, hours, errors]);

  useEffect(() => {
    if (!follow || hours) return;
    const t = setInterval(async () => {
      const params = new URLSearchParams({ n: "200" });
      if (query) params.set("q", query);
      if (last.current) params.set("since", last.current);
      try {
        const d = await api<{ Lines: Line[] }>("GET", `${path}?${params}`);
        const more = (d.Lines ?? []).filter((l) => l.Time > last.current || !last.current);
        if (more.length) {
          last.current = more[more.length - 1].Time;
          setLines((ls) => [...ls, ...more].slice(-1000));
        }
        setErr("");
      } catch (e) {
        setErr(errText(e));
      }
    }, 2000);
    return () => clearInterval(t);
  }, [follow, path, query, hours]);

  useLayoutEffect(() => {
    const b = body.current;
    if (b && follow && !hours && pinned.current) b.scrollTop = b.scrollHeight;
  }, [lines, follow, hours]);

  useEffect(() => {
    if (!full) return;
    const k = (e: KeyboardEvent) => e.key === "Escape" && setFull(false);
    document.addEventListener("keydown", k);
    return () => document.removeEventListener("keydown", k);
  }, [full]);

  const toTop = () => {
    pinned.current = false;
    if (body.current) body.current.scrollTop = 0;
  };
  const onScroll = () => {
    const b = body.current;
    if (b) pinned.current = b.scrollTop + b.clientHeight >= b.scrollHeight - 8;
  };

  const shown = lines.map((l) => ({ time: l.Time, text: l.Text, stderr: l.Stream === "stderr", bad: isFailureLine(l.Text), deploy: l.Deploy }))
    .filter((r) => !errors || r.bad || r.stderr);

  return (
    <div className="flex flex-col gap-3">
      {err && <Callout tone="bad">{err}</Callout>}
      <div className={"logview" + (full ? " full" : "")}>
        <Toolbar className="logbar w-full flex-wrap rounded-none shadow-none ring-0" aria-label="Log controls">
          <Select aria-label="From" className="w-auto rounded-none ring-0" value={hours} onValueChange={(v) => setHours(Number(v))}
            items={[{ value: 0, label: "This server" }, ...(drain ? [{ value: 24, label: "Loki, last 24 hours" }, { value: 168, label: "Loki, last 7 days" }] : [])]} />
          <Toolbar.InputGroup className="min-w-45 flex-1">
            <InputGroup.Addon><Icon name="search" /></InputGroup.Addon>
            <InputGroup.Input type="search" aria-label="Search" placeholder="Search the kept logs" value={q} onChange={(e) => setQ(e.target.value)} />
          </Toolbar.InputGroup>
          <div role="group" aria-label="View" className="flex items-center gap-1">
            <Toolbar.Button className={PRESSED} icon={<Icon name="flash" />} aria-pressed={follow} disabled={!!hours} focusableWhenDisabled={false} onClick={() => { pinned.current = true; setFollow(!follow); }}><span className={LABEL}>Live tail</span></Toolbar.Button>
            <Toolbar.Button className={PRESSED} icon={<Icon name="log-alert" />} aria-pressed={errors} onClick={() => setErrors(!errors)}><span className={LABEL}>Errors only</span></Toolbar.Button>
            <ZoneButton utc={utc} onUtc={onUtc} />
            <Toolbar.Button shape="square" aria-label="Jump to top" title="Jump to top" onClick={toTop}><Icon name="to-top" /></Toolbar.Button>
            <Toolbar.Button shape="square" className={PRESSED} aria-label="Full screen" aria-pressed={full} title={full ? "Leave full screen (Esc)" : "Full screen"} onClick={() => setFull(!full)}><Icon name="fullscreen" /></Toolbar.Button>
          </div>
        </Toolbar>
        <div className="logs" role="log" ref={body} onScroll={onScroll}>
          {!loaded ? <Skeleton /> : shown.length === 0 ? <div className="text-kumo-subtle">{lines.length ? "No error lines in this range." : "No lines."}</div> : <LogLines rows={shown} utc={utc} subjects={subjects} />}
        </div>
      </div>
      <span className="text-sm text-kumo-subtle">Secrets are masked before lines leave the server.</span>
      <LogAlerts project={project} env={env} service={service} />
    </div>
  );
}
