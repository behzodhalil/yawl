import { Suspense, lazy, useEffect, useState } from "react";
import { ArrowSquareOutIcon } from "@phosphor-icons/react";
import { Badge, Button, Checkbox, DropdownMenu, InlineCopyText, LayerCard, Loader, Select, Table, Tabs, Textarea, buttonVariants, cn, linkVariants } from "@cloudflare/kumo";
import { api } from "../api";
import { commitSubject, serviceWord, shortSha, took } from "../format";
import { fragment, navigate } from "../router";
import { Shell } from "../Shell";
import type { EnvView, ProjectView, SealedRow, ServerView } from "../types";
import { reseal } from "../reseal";
import { isDb } from "../serviceForm";
import { Canvas } from "./Canvas";
import { Ago, Callout, Empty, EnvName, Field, Icon, Link, Loading, Menu, MenuItem, Modal, NoSignal, StatusBadge, POLL_MS, errText, useBusy, useLoad, useReveal, ButtonLink, Input, Card } from "../ui";
import { ServerSelect, firstOnlineName } from "./NewProject";
import { DeleteDialog } from "../DeleteDialog";
import { Pager, SortTh, TableBar, useTable } from "../table";

export const KIND: Record<string, [string, string]> = { web: ["Web Service", "globe"], worker: ["Background Worker", "worker"], cron: ["Cron Job", "cron"], postgres: ["Postgres", "postgres"], valkey: ["Valkey", "valkey"], mysql: ["MySQL", "mysql"], mongodb: ["MongoDB", "mongodb"], mariadb: ["MariaDB", "mariadb"], clickhouse: ["ClickHouse", "clickhouse"] };

type CopyPlan = { lines: string[]; to_pubkey?: string; recovery_pubkey?: string; vars: SealedRow[] };

function AddStaging({ project, from, onClose }: { project: string; from?: EnvView; onClose: () => void }) {
  const servers = useLoad<ServerView[]>("/api/servers");
  const [picked, setPicked] = useState("");
  const server = picked || from?.server || (servers.data ? firstOnlineName(servers.data) : "");
  const [err, setErr] = useState("");
  const [plans, setPlans] = useState<[string, CopyPlan][] | null>(null);
  const [key, setKey] = useState("");
  const [busy, run] = useBusy();
  const order = from ? [...from.services].sort((x, y) => Number(isDb(y.kind)) - Number(isDb(x.kind))).map((x) => x.service) : [];
  const copy = (name: string, extra?: object) => api<CopyPlan>("POST", `/api/services/${project}/${from!.name}/${name}/copy${extra ? "" : "?dry=1"}`, { project, env: "staging", ...extra });
  const done = () => (onClose(), navigate(`/p/${project}/staging`));
  const copyAll = async (all: [string, CopyPlan][]) => {
    for (const [name, plan] of all) {
      const boxes = plan.vars.length > 0 ? (await reseal({ lines: plan.lines, to_pubkey: plan.to_pubkey!, recovery_pubkey: plan.recovery_pubkey!, restore: false, vars: plan.vars }, key)).boxes : {};
      await copy(name, { boxes, to_pubkey: plan.to_pubkey });
    }
    done();
  };
  const create = () => run(async () => {
    setErr("");
    try {
      if (plans) return await copyAll(plans);
      await api("POST", "/api/environments", { project, env: "staging", server });
      if (!from) return done();
      const all: [string, CopyPlan][] = [];
      for (const name of order) all.push([name, await copy(name)]);
      if (all.some(([, plan]) => plan.vars.length > 0)) return setPlans(all);
      await copyAll(all);
    } catch (e) {
      setErr(errText(e));
    }
  });
  const sealed = plans?.flatMap(([, plan]) => plan.vars) ?? [];
  return (
    <Modal title={from ? `Replicate ${from.name} to staging` : "Add staging"} onClose={onClose}>
      {from ? (
        <div className="flex flex-col gap-2">
          <p>{`Staging gets a copy of every service in ${from.name}, with its settings, volumes and variables. Nothing runs until you deploy it.`}</p>
          <ul className="flex list-disc flex-col gap-1 pl-5 text-kumo-subtle">
            <li>Databases start empty, with their own password; restore a backup into them for their data.</li>
            <li>{`Variables that reference a database point at staging's copy; references to another environment are left out.`}</li>
            <li>{`Domains and outside ports stay with ${from.name}.`}</li>
            <li>On another server, variables are sealed again in this browser with the recovery key.</li>
          </ul>
          <p className="font-mono text-sm text-kumo-subtle">{order.join(", ")}</p>
        </div>
      ) : <p className="text-kumo-subtle">Staging is a second copy of the project, with its own variables and database. It can live on another server.</p>}
      {!plans && (!servers.data ? <Loading what={servers} /> : !server ? <Callout tone="warn">No server is online.</Callout> : <ServerSelect servers={servers.data} value={server} onChange={setPicked} />)}
      {plans && (
        <Field label="Recovery key" hint={`${sealed.length} variable(s) are sealed again for ${server}, here in this browser.`}>
          {(id) => <Textarea id={id} className="w-full font-mono text-sm" rows={2} autoComplete="off" spellCheck={false} value={key} onChange={(x) => setKey(x.target.value)} />}
        </Field>
      )}
      {err && <Callout tone="bad">{err}</Callout>}
      <div className="flex flex-wrap items-center gap-2.5">
        {server && <Button variant="primary" disabled={busy || (!!plans && !key.trim())} onClick={create}>{plans ? "Copy the services" : from ? "Replicate to staging" : "Create staging"}</Button>}
        {busy && <Loader size="sm" />}
        <Button variant="ghost" onClick={onClose}>Cancel</Button>
      </div>
    </Modal>
  );
}

const YamlView = lazy(() => import("./YamlView"));

async function loadYaml(project: string, env: string): Promise<string> {
  const res = await fetch(`/api/environments/${project}/${env}/export`);
  const text = await res.text();
  if (!res.ok) throw new Error(text.trim() || res.statusText);
  return text;
}

function download(text: string) {
  const url = URL.createObjectURL(new Blob([text], { type: "application/yaml" }));
  const a = document.createElement("a");
  a.href = url;
  a.download = "yawl.yaml";
  a.click();
  URL.revokeObjectURL(url);
}

function ExportDialog({ yaml, onClose }: { yaml: string; onClose: () => void }) {
  return (
    <Modal title="yawl.yaml" size="xl" onClose={onClose}>
      <p className="text-kumo-subtle">This environment as a file. Keep it in the repository; the CLI applies it with yawl apply.</p>
      <Suspense fallback={<Loader />}><YamlView code={yaml} /></Suspense>
      <div className="flex flex-wrap items-center gap-2.5">
        <Button variant="primary" onClick={() => download(yaml)}>Download yawl.yaml</Button>
      </div>
    </Modal>
  );
}

type View = "table" | "canvas";
const VIEW = "yawl.projectView";

function keptView(): View {
  try { return localStorage.getItem(VIEW) === "canvas" ? "canvas" : "table"; } catch { return "table"; }
}

function keepView(v: View) {
  try { localStorage.setItem(VIEW, v); } catch { /* storage off */ }
}

export function firstEnv(p: ProjectView): string {
  const own = p.envs.filter((x) => !x.preview_of);
  return (own.find((x) => x.name === "production") ?? own[0])?.name ?? "production";
}

export function Project({ project, env, pulls = false }: { project: string; env: string; pulls?: boolean }) {
  const tree = useLoad<ProjectView[]>("/api/projects", POLL_MS);
  const me = useLoad<{ role: string }>("/api/me");
  const canEdit = me.data !== undefined && me.data.role !== "viewer";
  const [adding, setAdding] = useState<"" | "empty" | "copy">("");
  const [view, setView] = useState(keptView);
  const [deleting, setDeleting] = useState<"" | "env" | "project">("");
  const [exportErr, setExportErr] = useState("");
  const [yaml, setYaml] = useState<string | null>(null);
  const [tag, setTag] = useState("");
  const p = tree.data?.find((x) => x.name === project);
  const e = p?.envs.find((x) => x.name === env);
  const tags = [...new Set(e?.services.flatMap((s) => s.tags ?? []) ?? [])].sort();
  const shown = e?.services.filter((s) => !tag || s.tags?.includes(tag)) ?? [];
  const tbl = useTable(shown, { text: (s) => [s.service, s.kind, s.url ?? "", ...(s.tags ?? [])].join(" "), sort: { service: (s) => s.service, status: (s) => s.state } });
  const base = `/p/${project}/${env}`;
  const fallback = p && !e && !pulls ? firstEnv(p) : "";
  useEffect(() => {
    if (fallback && fallback !== env) navigate(`/p/${project}/${fallback}`, true);
  }, [fallback, env, project]);
  const source = p?.envs.find((x) => x.preview);
  const previews = useLoad<PreviewRow[]>(source ? `/api/projects/${project}/previews` : null, POLL_MS);
  const mine = (previews.data ?? []).filter((x) => x.source === source?.name);
  const open = mine.filter((x) => !x.merged).length;
  return (
    <Shell crumb={[<Link to="/">Projects</Link>, project, <EnvName env={env} />]} active="/">
      {!tree.data ? <Loading what={tree} /> : !p ? <Callout tone="warn" title={`There is no project ${project}.`} /> : (
        <>
          {exportErr && <Callout tone="bad">{exportErr}</Callout>}
          {yaml !== null && <ExportDialog yaml={yaml} onClose={() => setYaml(null)} />}
          <div className="flex flex-wrap items-end justify-between gap-4">
            <div><div className="flex flex-wrap items-center gap-x-3.5 gap-y-1.5"><h1>{project}</h1>{p.envs.length > 0 && (
              <Menu label={`Switch environment, ${pulls ? "Pull requests" : env}`} alignStart wide trigger={<>{pulls ? "Pull requests" : <EnvName env={env} />}<Icon name="down" /></>}>
                <DropdownMenu.RadioGroup value={pulls ? "" : env} onValueChange={(v: string) => navigate(`/p/${project}/${v}`)}>
                  <DropdownMenu.Label>Environments</DropdownMenu.Label>
                  {p.envs.filter((x) => !x.preview_of).map((x) => (
                    <DropdownMenu.RadioItem key={x.name} value={x.name} closeOnClick>
                      <EnvName env={x.name} /><span className="ml-auto text-kumo-subtle">{x.server}</span><DropdownMenu.RadioItemIndicator keepMounted className="ml-2 data-[unchecked]:invisible" />
                    </DropdownMenu.RadioItem>
                  ))}
                </DropdownMenu.RadioGroup>
                {source && (
                  <DropdownMenu.Group>
                    <DropdownMenu.Separator />
                    <DropdownMenu.Label>Previews</DropdownMenu.Label>
                    <MenuItem to={`/p/${project}/${source.name}/pulls`} current={pulls}>Pull requests{open > 0 && <Badge variant="secondary">{open}</Badge>}</MenuItem>
                  </DropdownMenu.Group>
                )}
                {!p.envs.some((x) => x.name === "staging") && <><DropdownMenu.Separator /><MenuItem icon="plus" onClick={() => setAdding("empty")}>Add staging</MenuItem></>}
              </Menu>
            )}</div>{e && (e.server_offline
              ? <p className="mt-1 text-kumo-subtle">{env} runs on <Link to={`/servers/${encodeURIComponent(e.server)}`} className={linkVariants({ variant: "inline" })}>{e.server}</Link>, offline, last seen <Ago ms={e.server_last_seen} />.</p>
              : <p className="mt-1 text-kumo-subtle">{env} runs on {e.server}.</p>)}</div>
            {e && <div className="flex flex-wrap items-center gap-2.5">
              <ButtonLink to={`${base}/new`} variant="primary" icon={<Icon name="plus" />}>New service</ButtonLink>
              <Menu label="More actions" trigger={<Icon name="more" />}>
                <MenuItem icon="code" description="This environment as a file to keep in the repository." onClick={() => (setExportErr(""), loadYaml(project, env).then(setYaml, (x) => setExportErr(errText(x))))}>Export yawl.yaml</MenuItem>
                <MenuItem icon="server" description="To another server, data included." to={`${base}/move`}>{`Move ${env}`}</MenuItem>
                {env !== "staging" && !e.preview_of && !p.envs.some((x) => x.name === "staging") && e.services.length > 0 && <MenuItem icon="layers" description="Every service and its settings; databases start empty." onClick={() => setAdding("copy")}>Replicate to staging…</MenuItem>}
                <DropdownMenu.Separator />
                <MenuItem danger onClick={() => setDeleting("env")}>{`Delete ${env}`}</MenuItem>
                <MenuItem danger onClick={() => setDeleting("project")}>Delete project</MenuItem>
              </Menu>
            </div>}
            {deleting && (
              <DeleteDialog what={deleting === "env" ? env : project} path={deleting === "env" ? `/api/environments/${project}/${env}` : `/api/projects/${project}`}
                purgeAt={Date.now() + 48 * 3_600_000} onClose={() => setDeleting("")} onDone={() => navigate("/activity/trash")} />
            )}
          </div>
          {pulls ? (
            <PullRequests project={project} rows={mine} load={previews} />
          ) : !e ? <Callout tone="warn" title={`${project} has no ${env} environment.`} /> : e.services.length === 0 ? (
            <Empty text="No services yet. New service, above, adds the first." />
          ) : (
            <div className="flex flex-col gap-3">
            <div role="group" aria-label="View" className="self-start">
              <Tabs variant="segmented" size="sm" tabs={[{ value: "table", label: "Table" }, { value: "canvas", label: "Canvas" }]} value={view} onValueChange={(v) => { setView(v as View); keepView(v as View); }} />
            </div>
            {view === "canvas" ? <Canvas project={project} env={e} base={base} /> : <>
            {tags.length > 0 && (
              <div className="flex flex-wrap items-center gap-2.5">
                <Field label="Tag">{(id) => (
                  <Select aria-labelledby={id + "-label"} value={tag} onValueChange={(v) => setTag(String(v))}
                    items={{ "": "All services", ...Object.fromEntries(tags.map((t) => [t, t])) }} />
                )}</Field>
              </div>
            )}
            <TableBar t={tbl} noun="services" find="Search services" />
            <LayerCard className="overflow-x-auto p-0">
              <Table>
                <Table.Header><Table.Row><SortTh t={tbl} k="service">Service</SortTh><SortTh t={tbl} k="status">Status</SortTh><Table.Head>Address</Table.Head><Table.Head>Last deploy</Table.Head><Table.Head><span className="sr-only">Actions</span></Table.Head></Table.Row></Table.Header>
                <Table.Body>
                  {tbl.rows.map((s) => (
                    <Table.Row key={s.service}>
                      <Table.Cell>
                        <div className="flex items-center gap-2.5">
                          <span title={KIND[s.kind]?.[0] ?? s.kind}><Icon name={KIND[s.kind]?.[1] ?? "box"} /></span>
                          <div><Link to={`${base}/s/${s.service}`}><b className="font-semibold">{s.service}</b></Link><div className="text-kumo-subtle"><span className="whitespace-nowrap max-sm:sr-only">{KIND[s.kind]?.[0] ?? s.kind}</span>{s.tags?.map((t) => <span key={t} className="ml-1.5 font-mono text-sm">{t}</span>)}</div></div>
                        </div>
                      </Table.Cell>
                      <Table.Cell>
                        <div className="flex flex-col gap-1">
                          <div className="flex flex-wrap items-center gap-1.5">
                            {e?.server_offline
                              ? <NoSignal lastSeen={e.server_last_seen} />
                              : <StatusBadge kind={serviceWord(s.kind, s.state)[1]}>{serviceWord(s.kind, s.state)[0]}</StatusBadge>}
                            {s.health === "down" && <StatusBadge kind="bad">Down</StatusBadge>}
                          </div>
                          {s.cause && <span className="text-kumo-subtle">{s.cause}</span>}
                        </div>
                      </Table.Cell>
                      <Table.Cell>{s.url
                        ? <InlineCopyText value={s.url} labels={{ copyAction: `Copy the address of ${s.service}`, copied: "Address copied" }}><span title={s.url}>{host(s.url)}</span></InlineCopyText>
                        : <span className="text-kumo-subtle">{noAddress(s.kind, s.service)}</span>}</Table.Cell>
                      <Table.Cell>{s.commit || s.at ? (
                        <div className="flex flex-col gap-0.5">
                          <span>{s.at ? <Ago ms={s.at} /> : null}{s.commit && <>{s.at ? " · " : null}{s.deploy_id ? <Link to={`${base}/s/${s.service}/deploys/${s.deploy_id}`} className="font-mono text-sm text-kumo-subtle underline underline-offset-3">{shortSha(s.commit)}</Link> : <span className="font-mono text-sm text-kumo-subtle">{shortSha(s.commit)}</span>}</>}</span>
                          {s.subject && <span className="max-w-[36ch] truncate text-sm text-kumo-subtle">{commitSubject(s.subject)}</span>}
                        </div>
                      ) : <span className="text-kumo-subtle">—</span>}</Table.Cell>
                      <Table.Cell className="w-0">{s.url && (
                        <div className="flex items-center justify-end gap-1.5">
                          <a href={s.url} target="_blank" rel="noreferrer" aria-label={`Open ${host(s.url)}`} title="Open in a new tab" className={buttonVariants({ variant: "ghost", size: "sm", shape: "square" })}><ArrowSquareOutIcon size={16} aria-hidden="true" /></a>
                          {canEdit && <Button size="sm" variant="ghost" shape="square" title={`Edit the domains of ${s.service}`} aria-label={`Edit the domains of ${s.service}`} onClick={() => navigate(`${base}/s/${s.service}/settings#domains`)}><Icon name="pencil" /></Button>}
                        </div>
                      )}</Table.Cell>
                    </Table.Row>
                  ))}
                </Table.Body>
              </Table>
            </LayerCard>
            <Pager page={tbl.page} pages={tbl.pages} onPage={tbl.setPage} />
            </>}
            </div>
          )}
        </>
      )}
      {e && !pulls && e.preview && <PreviewStrip project={project} env={e} open={open} admin={me.data?.role === "admin"} />}
      {adding && <AddStaging project={project} from={adding === "copy" ? e : undefined} onClose={() => setAdding("")} />}
    </Shell>
  );
}

type Step = { name: string; state: "done" | "run" | "fail" | "todo"; ms?: number };
type PreviewRow = {
  id: string; number: number; title: string; branch: string; state: string; source: string; env?: string; comment?: string;
  base?: string; author?: string; head?: string; at: number; merged?: boolean;
  service?: string; deploy_id?: string; url?: string; commit?: string; cause?: string; steps?: Step[]; landed?: [string, string][];
};

const CLOSABLE = ["waiting", "making", "live", "failed"];

function noAddress(kind: string, service: string): string {
  if (kind === "worker") return "No address: it calls out";
  if (kind === "cron") return "Runs on its schedule";
  if (KIND[kind] && kind !== "web") return `${service} · private network`;
  return "No address yet";
}

const host = (url: string) => url.replace(/^https?:\/\//, "");

function PreviewStrip({ project, env, open, admin }: { project: string; env: EnvView; open: number; admin: boolean }) {
  const [shown, setShown] = useState(admin && fragment() === "previews");
  const [err, setErr] = useState("");
  const reveal = useReveal<HTMLDivElement>(shown);
  useEffect(() => { if (shown && fragment() === "previews") document.getElementById("previews")?.scrollIntoView?.(); }, []);
  const copied = env.services.filter((s) => !s.no_preview), left = env.services.filter((s) => s.no_preview);
  return (
    <div className="mt-4 flex flex-col gap-3">
      <LayerCard className="flex flex-wrap items-center justify-between gap-x-5 gap-y-3 px-4 py-3">
        <div className="flex flex-wrap items-center gap-2">
          <span className="text-kumo-subtle">Each pull request gets a copy of</span>
          <ul aria-label="Copied" className="flex flex-wrap gap-1.5">{copied.map((s) => <li key={s.service}><Badge variant="outline" className="font-mono">{s.service}</Badge></li>)}</ul>
          {left.length > 0 && <span className="text-kumo-subtle">· not copied</span>}
          {left.length > 0 && <ul aria-label="Not copied" className="flex flex-wrap gap-1.5">{left.map((s) => <li key={s.service}><Badge variant="secondary" className="font-mono text-kumo-subtle line-through">{s.service}</Badge></li>)}</ul>}
        </div>
        <div className="flex flex-wrap items-center gap-2.5">
          {admin && <Button size="sm" aria-expanded={shown} onClick={() => setShown(!shown)}>Preview settings</Button>}
          <ButtonLink to={`/p/${project}/${env.name}/pulls`} size="sm">{open === 1 ? "1 open pull request" : `${open} open pull requests`}</ButtonLink>
        </div>
      </LayerCard>
      {shown && (
        <div id="previews" ref={reveal}>
        <Card title="What a pull request copies">
          {err && <Callout tone="bad">{err}</Callout>}
          {env.services.map((s) => (
            <Checkbox key={s.service} checked={!s.no_preview} onCheckedChange={async (on) => {
              setErr("");
              try { await api("PUT", `/api/services/${project}/${env.name}/${s.service}/preview`, { on }); } catch (x) { setErr(errText(x)); }
            }} label={<span className="flex flex-wrap items-center gap-2.5"><b className="font-semibold">{s.service}</b><span className="text-kumo-subtle">{KIND[s.kind]?.[0] ?? s.kind}</span></span>} />
          ))}
          <p className="text-kumo-subtle">Off is <code>preview: false</code> in yawl.yaml: a worker with a bot token, or a slow build, stays out. It applies to previews made from now.</p>
          <PreviewSenders project={project} />
        </Card>
        </div>
      )}
    </div>
  );
}

function PullRequests({ project, rows, load }: { project: string; rows: PreviewRow[]; load: { data?: PreviewRow[]; error?: string; reload: () => void } }) {
  const [which, setWhich] = useState<"open" | "merged">("open");
  const [err, setErr] = useState("");
  const merged = rows.filter((r) => r.merged), open = rows.filter((r) => !r.merged);
  const list = which === "open" ? open : merged;
  if (!load.data) return <Loading what={load} />;
  return (
    <div className="mt-4 flex flex-col gap-3">
      <Tabs variant="segmented" className="self-start" value={which} onValueChange={(v) => setWhich(v as "open" | "merged")}
        tabs={[{ value: "open", label: `Open · ${open.length}` }, { value: "merged", label: `Merged, last day · ${merged.length}` }]} />
      {err && <Callout tone="bad">{err}</Callout>}
      {list.length === 0 ? (
        <Empty text={which === "open" ? "No open pull requests. One opened on a repository this environment builds gets its preview here in about a minute." : "Nothing merged in the last day."} />
      ) : (
        <LayerCard className="flex flex-col divide-y divide-kumo-hairline p-0">
          {list.map((r) => <PullCard key={r.id} project={project} r={r} onClose={async () => {
            setErr("");
            try { await api("POST", `/api/previews/${r.id}/close`); } catch (e) { setErr(errText(e)); }
            load.reload();
          }} />)}
        </LayerCard>
      )}
    </div>
  );
}

const STEP: Record<string, string> = { build: "Build", deploy: "Deploy", live: "Live" };

const STEP_MARK: Record<Step["state"], string> = { done: "✓", fail: "✕", run: "…", todo: "" };
const STEP_TONE: Record<Step["state"], string> = { done: "text-kumo-success", fail: "text-kumo-danger", run: "text-kumo-default", todo: "text-kumo-subtle" };

function PullCard({ project, r, onClose }: { project: string; r: PreviewRow; onClose: () => void }) {
  const logs = r.env && r.service && r.deploy_id ? `/p/${project}/${r.env}/s/${r.service}/deploys/${r.deploy_id}` : "";
  const running = r.steps?.some((s) => s.state === "run"), failed = r.steps?.some((s) => s.state === "fail") || r.state === "failed";
  const sha = r.commit || r.head;
  return (
    <article className="grid grid-cols-[minmax(0,1fr)_auto] gap-x-6 gap-y-3 px-4.5 py-4">
      <div className="flex min-w-0 flex-col gap-1.5">
        <div className="flex flex-wrap items-center gap-2.5"><span className="font-mono text-sm tabular-nums text-kumo-subtle">#{r.number}</span><b className="font-semibold">{r.title}</b></div>
        <div className="flex flex-wrap items-center gap-2 font-mono text-sm text-kumo-subtle">
          <span>{r.branch}</span>{r.base && <><span aria-hidden="true">→</span><span>{r.base}</span></>}{r.author && <span>· {r.author}</span>}
        </div>
        {r.landed ? (
          <Table className="w-auto"><Table.Body>
            {r.landed.map(([k, v]) => <Table.Row key={k}><Table.Cell className="font-mono text-sm text-kumo-subtle">{k}</Table.Cell><Table.Cell>{v}</Table.Cell></Table.Row>)}
          </Table.Body></Table>
        ) : r.steps ? (
          <ol className="mt-0.5 flex flex-wrap items-center gap-y-1.5" aria-label="Steps">
            {r.steps.map((s, i) => (
              <li key={s.name} className={cn("inline-flex items-center gap-1.5 text-sm", STEP_TONE[s.state])}>
                {i > 0 && <span aria-hidden="true" className="mx-2 h-px w-4.5 bg-kumo-line" />}
                <i aria-hidden="true" className="w-4 not-italic">{STEP_MARK[s.state]}</i>{STEP[s.name] ?? s.name}{s.ms ? " " + took(s.ms) : ""}<span className="sr-only">{`: ${s.state}`}</span>
              </li>
            ))}
          </ol>
        ) : r.comment ? <span className="text-kumo-subtle">{r.comment}</span> : null}
        {r.cause && <span className="text-kumo-danger">{r.cause}</span>}
        <div className="flex flex-wrap items-center gap-3.5 text-sm text-kumo-subtle">
          {sha && <span className="font-mono tabular-nums">{shortSha(sha)}</span>}
          {r.at > 0 && <span>{r.merged ? "merged " : "pushed "}<Ago ms={r.at} /></span>}
          {r.state === "live" && !r.merged && <span>removed after 7 days without a push</span>}
        </div>
      </div>
      <div className="flex flex-wrap items-start justify-end gap-2.5">
        {r.url && !r.merged && <ButtonLink variant="primary" to={r.url} target="_blank" rel="noreferrer">Open preview ↗</ButtonLink>}
        {!r.url && logs && running && <ButtonLink variant="primary" to={logs}>Watch build</ButtonLink>}
        {!r.url && logs && failed && !running && <ButtonLink variant="primary" to={logs}>See why</ButtonLink>}
        {r.url && logs && !r.merged && <ButtonLink variant="secondary" to={logs}>Logs</ButtonLink>}
        {CLOSABLE.includes(r.state) && !r.merged && <Button variant="ghost" aria-label={`Close preview ${r.number}`} onClick={onClose}>Close</Button>}
      </div>
    </article>
  );
}

const SENDERS_HINT = "preview-senders-hint";

function PreviewSenders({ project }: { project: string }) {
  const got = useLoad<{ senders: string[] }>(`/api/projects/${project}/preview-senders`);
  const [text, setText] = useState<string | null>(null);
  const [msg, setMsg] = useState("");
  const value = text ?? (got.data?.senders ?? []).join(" ");
  return (
    <form className="flex flex-col gap-1.5" onSubmit={async (ev) => {
      ev.preventDefault();
      setMsg("");
      try {
        await api("PUT", `/api/projects/${project}/preview-senders`, { senders: value.split(/[\s,]+/).filter(Boolean) });
        setText(null);
        got.reload();
        setMsg("Saved.");
      } catch (e) { setMsg(errText(e)); }
    }}>
      <div className="flex flex-wrap items-end gap-2.5">
        <Field label="Who may build previews">
          {(id) => <Input id={id} className="font-mono" aria-describedby={SENDERS_HINT} value={value} onChange={(e) => setText(e.target.value)} />}
        </Field>
        <Button size="sm" type="submit">Save</Button>
        {msg && <span className="text-kumo-subtle">{msg}</span>}
      </div>
      <span id={SENDERS_HINT} className="text-sm text-kumo-subtle">Forge logins, like octocat or dependabot[bot]. A pull request or push from anyone else is held. Empty: anyone who may push to the repository.</span>
    </form>
  );
}
