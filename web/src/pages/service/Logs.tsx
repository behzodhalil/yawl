import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { api } from "../../api";
import { LogLines, zoneName } from "../../LogView";
import { LogAlerts } from "./LogAlerts";
import { Callout, Icon, Skeleton, errText } from "../../ui";

type Line = { Time: string; Deploy: string; Text: string; Stream?: "stderr" };

const UTC_KEY = "yawl.logs.utc";

export function Logs({ project, env, service }: { project: string; env: string; service: string }) {
  const path = `/api/services/${project}/${env}/${service}/logs`;
  const [lines, setLines] = useState<Line[]>([]);
  const [q, setQ] = useState("");
  const [query, setQuery] = useState("");
  const [follow, setFollow] = useState(false);
  const [errors, setErrors] = useState(false);
  const [err, setErr] = useState("");
  const [loaded, setLoaded] = useState(false);
  const [drain, setDrain] = useState(false);
  const [hours, setHours] = useState(0);
  const [utc, setUtc] = useState(() => { try { return localStorage.getItem(UTC_KEY) === "1"; } catch { return false; } });
  const [full, setFull] = useState(false);
  const last = useRef("");
  const body = useRef<HTMLDivElement>(null);
  const pinned = useRef(true);

  useEffect(() => {
    const t = setTimeout(() => setQuery(q), 300);
    return () => clearTimeout(t);
  }, [q]);

  useEffect(() => {
    let live = true;
    const params = new URLSearchParams({ n: hours ? "1000" : "200" });
    if (query) params.set("q", query);
    if (errors) params.set("stream", "stderr");
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
      if (errors) params.set("stream", "stderr");
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
  }, [follow, path, query, hours, errors]);

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

  const toggleUtc = () => {
    setUtc(!utc);
    try { localStorage.setItem(UTC_KEY, utc ? "0" : "1"); } catch { /* storage off */ }
  };
  const toTop = () => {
    pinned.current = false;
    if (body.current) body.current.scrollTop = 0;
  };
  const onScroll = () => {
    const b = body.current;
    if (b) pinned.current = b.scrollTop + b.clientHeight >= b.scrollHeight - 8;
  };

  return (
    <div className="col">
      {err && <Callout tone="bad">{err}</Callout>}
      <div className={"logview" + (full ? " full" : "")}>
        <div className="logbar">
          <select aria-label="From" value={hours} onChange={(e) => setHours(Number(e.target.value))}>
            <option value={0}>This server</option>
            {drain && <option value={24}>Loki, last 24 hours</option>}
            {drain && <option value={168}>Loki, last 7 days</option>}
          </select>
          <label className="tsearch">
            <Icon name="search" /><input type="search" aria-label="Search" placeholder="Search the kept logs" value={q} onChange={(e) => setQ(e.target.value)} />
          </label>
          <button className="btn" aria-pressed={follow} disabled={!!hours} onClick={() => { pinned.current = true; setFollow(!follow); }}>
            <Icon name="flash" />Live tail
          </button>
          <button className="btn" aria-pressed={errors} onClick={() => setErrors(!errors)}>
            <Icon name="log-alert" />Errors only
          </button>
          <button className="btn" aria-label={`Times in ${zoneName(utc)}`} title={utc ? "Show local times" : "Show UTC times"} onClick={toggleUtc}>{zoneName(utc)}</button>
          <button className="btn" aria-label="Jump to top" title="Jump to top" onClick={toTop}><Icon name="to-top" /></button>
          <button className="btn" aria-label="Full screen" aria-pressed={full} title={full ? "Leave full screen (Esc)" : "Full screen"} onClick={() => setFull(!full)}><Icon name="fullscreen" /></button>
        </div>
        <div className="logs" role="log" ref={body} onScroll={onScroll}>
          {!loaded ? <Skeleton /> : lines.length === 0 ? <div className="dim">No lines.</div> : <LogLines rows={lines.map((l) => ({ time: l.Time, text: l.Text, stderr: l.Stream === "stderr" }))} utc={utc} />}
        </div>
      </div>
      <span className="muted" style={{ fontSize: 13 }}>Secrets are masked before lines leave the server.</span>
      <LogAlerts project={project} env={env} service={service} />
    </div>
  );
}
