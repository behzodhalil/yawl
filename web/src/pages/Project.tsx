import { useState } from "react";
import { api } from "../api";
import { ago, serviceWord, shortSha, took } from "../format";
import { navigate } from "../router";
import { Shell } from "../Shell";
import type { EnvView, ProjectView, ServerView } from "../types";
import { Callout, Empty, EnvName, Field, Icon, Link, Loading, Menu, MenuItem, Modal, StatusBadge, POLL_MS, errText, useBusy, useLoad } from "../ui";
import { ServerSelect } from "./NewProject";
import { DeleteDialog } from "../DeleteDialog";
import { Pager, SortTh, TableBar, useTable } from "../table";

export const KIND: Record<string, [string, string]> = { web: ["Web Service", "globe"], worker: ["Background Worker", "worker"], cron: ["Cron Job", "cron"], postgres: ["Postgres", "postgres"], valkey: ["Valkey", "valkey"], mysql: ["MySQL", "mysql"], mongodb: ["MongoDB", "mongodb"], mariadb: ["MariaDB", "mariadb"], clickhouse: ["ClickHouse", "clickhouse"] };

function AddStaging({ project, onClose }: { project: string; onClose: () => void }) {
  const servers = useLoad<ServerView[]>("/api/servers");
  const [picked, setPicked] = useState("");
  const server = picked || servers.data?.[0]?.name || "";
  const [err, setErr] = useState("");
  const [busy, run] = useBusy();
  return (
    <Modal title="Add staging" onClose={onClose}>
      <p className="muted">Staging is a second copy of the project, with its own variables and database. It can live on another server.</p>
      {!servers.data ? <Loading what={servers} /> : <ServerSelect servers={servers.data} value={server} onChange={setPicked} />}
      {err && <Callout tone="bad">{err}</Callout>}
      <div className="row">
        <button className="btn pri" disabled={busy} onClick={() => run(async () => {
          try {
            await api("POST", "/api/environments", { project, env: "staging", server });
            onClose();
            navigate(`/p/${project}/staging`);
          } catch (e) {
            setErr(errText(e));
          }
        })}>Create staging</button>
        <button className="btn ghost" onClick={onClose}>Cancel</button>
      </div>
    </Modal>
  );
}

async function exportYaml(project: string, env: string) {
  const res = await fetch(`/api/environments/${project}/${env}/export`);
  const text = await res.text();
  if (!res.ok) throw new Error(text.trim() || res.statusText);
  const url = URL.createObjectURL(new Blob([text], { type: "application/yaml" }));
  const a = document.createElement("a");
  a.href = url;
  a.download = "yawl.yaml";
  a.click();
  URL.revokeObjectURL(url);
}

export function Project({ project, env, pulls = false }: { project: string; env: string; pulls?: boolean }) {
  const tree = useLoad<ProjectView[]>("/api/projects", POLL_MS);
  const me = useLoad<{ role: string }>("/api/me");
  const canEdit = me.data !== undefined && me.data.role !== "viewer";
  const [adding, setAdding] = useState(false);
  const [deleting, setDeleting] = useState<"" | "env" | "project">("");
  const [exportErr, setExportErr] = useState("");
  const [tag, setTag] = useState("");
  const p = tree.data?.find((x) => x.name === project);
  const e = p?.envs.find((x) => x.name === env);
  const tags = [...new Set(e?.services.flatMap((s) => s.tags ?? []) ?? [])].sort();
  const shown = e?.services.filter((s) => !tag || s.tags?.includes(tag)) ?? [];
  const tbl = useTable(shown, { text: (s) => [s.service, s.kind, s.url ?? "", ...(s.tags ?? [])].join(" "), sort: { service: (s) => s.service, status: (s) => s.state } });
  const base = `/p/${project}/${env}`;
  const source = p?.envs.find((x) => x.preview);
  const previews = useLoad<PreviewRow[]>(source ? `/api/projects/${project}/previews` : null, POLL_MS);
  const mine = (previews.data ?? []).filter((x) => x.source === source?.name);
  const open = mine.filter((x) => !x.merged).length;
  return (
    <Shell crumb={[<Link to="/">Projects</Link>, project, <EnvName env={env} />]} active="/">
      {!tree.data ? <Loading what={tree} /> : !p ? <Callout tone="warn" title={`There is no project ${project}.`} /> : (
        <>
          {exportErr && <Callout tone="bad">{exportErr}</Callout>}
          <div className="ph">
            <div><h1>{project}</h1>{e && <p className="sub">{env} runs on {e.server}.</p>}</div>
            {e && <div className="row">
              <Link to={`${base}/new`} className="btn pri"><Icon name="plus" />New service</Link>
              <Menu label="More actions" trigger={<Icon name="more" />}>
                <MenuItem onClick={() => (setExportErr(""), exportYaml(project, env).catch((x) => setExportErr(errText(x))))}>Export yawl.yaml</MenuItem>
                <MenuItem to={`${base}/move`}>{`Move ${env}`}</MenuItem>
                {!p.envs.some((x) => x.name === "staging") && <MenuItem onClick={() => setAdding(true)}>Add staging</MenuItem>}
                <hr />
                <MenuItem danger onClick={() => setDeleting("env")}>{`Delete ${env}`}</MenuItem>
                <MenuItem danger onClick={() => setDeleting("project")}>Delete project</MenuItem>
              </Menu>
            </div>}
            {deleting && (
              <DeleteDialog what={deleting === "env" ? env : project} path={deleting === "env" ? `/api/environments/${project}/${env}` : `/api/projects/${project}`}
                purgeAt={Date.now() + 48 * 3_600_000} onClose={() => setDeleting("")} onDone={() => navigate("/activity/trash")} />
            )}
          </div>
          <div className="tabs">
            {p.envs.filter((x) => !x.preview_of).map((x) => <Link key={x.name} to={`/p/${project}/${x.name}`} className={x.name === env && !pulls ? "on" : ""}><EnvName env={x.name} /></Link>)}
            {source && (
              <Link to={`/p/${project}/${source.name}/pulls`} className={"tab-end" + (pulls ? " on" : "")}>Pull requests{open > 0 && <span className="count">{open}</span>}</Link>
            )}
          </div>
          {pulls ? (
            <PullRequests project={project} rows={mine} load={previews} />
          ) : !e ? <Callout tone="warn" title={`${project} has no ${env} environment.`} /> : e.services.length === 0 ? (
            <Empty text="No services yet. New service, above, adds the first." />
          ) : (
            <div className="col">
            {tags.length > 0 && (
              <div className="row">
                <Field label="Tag">{(id) => (
                  <select id={id} className="in" value={tag} onChange={(ev) => setTag(ev.target.value)}>
                    <option value="">All services</option>
                    {tags.map((t) => <option key={t} value={t}>{t}</option>)}
                  </select>
                )}</Field>
              </div>
            )}
            <TableBar t={tbl} noun="services" find="Search services" />
            <div className="card scroll">
              <table className="tbl">
                <thead><tr><SortTh t={tbl} k="service">Service</SortTh><SortTh t={tbl} k="status">Status</SortTh><th>Address</th><th>Last deploy</th></tr></thead>
                <tbody>
                  {tbl.rows.map((s) => (
                    <tr key={s.service}>
                      <td>
                        <div className="row" style={{ flexWrap: "nowrap" }}>
                          <Icon name={KIND[s.kind]?.[1] ?? "box"} />
                          <div><Link to={`${base}/s/${s.service}`}><b>{s.service}</b></Link><div className="dim">{KIND[s.kind]?.[0] ?? s.kind}{s.tags?.map((t) => <span key={t} className="mono" style={{ marginLeft: 6 }}>{t}</span>)}</div></div>
                        </div>
                      </td>
                      <td>
                        <div className="col" style={{ gap: 4 }}>
                          <div className="row" style={{ gap: 6 }}>
                            <StatusBadge kind={serviceWord(s.kind, s.state)[1]}>{serviceWord(s.kind, s.state)[0]}</StatusBadge>
                            {s.health === "down" && <StatusBadge kind="bad">Down</StatusBadge>}
                          </div>
                          {s.cause && <span className="muted">{s.cause}</span>}
                        </div>
                      </td>
                      <td>{s.url ? (
                        <div className="row" style={{ gap: 6, flexWrap: "nowrap" }}>
                          <a href={s.url} target="_blank" rel="noreferrer" className="mono out">{s.url.replace(/^https?:\/\//, "")}</a>
                          <CopyButton text={s.url} label={`Copy the address of ${s.service}`} />
                          {canEdit && <button type="button" className="btn sm ghost" aria-label={`Edit the domains of ${s.service}`} onClick={() => navigate(`${base}/s/${s.service}/settings#domains`)}><Icon name="cog" /></button>}
                        </div>
                      ) : <span className="dim">{noAddress(s.kind, s.service)}</span>}</td>
                      <td>{s.commit && (
                        <div className="col" style={{ gap: 2 }}>
                          {s.deploy_id ? <Link to={`${base}/s/${s.service}/deploys/${s.deploy_id}`} className="num ul">{shortSha(s.commit)}</Link> : <span className="num">{shortSha(s.commit)}</span>}
                          {(s.subject || s.at) && <span className="dim clip">{[s.subject, s.at && ago(s.at)].filter(Boolean).join(" · ")}</span>}
                        </div>
                      )}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
            <Pager page={tbl.page} pages={tbl.pages} onPage={tbl.setPage} />
            </div>
          )}
        </>
      )}
      {e && !pulls && e.preview && <PreviewStrip project={project} env={e} open={open} admin={me.data?.role === "admin"} />}
      {adding && <AddStaging project={project} onClose={() => setAdding(false)} />}
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

function CopyButton({ text, label }: { text: string; label: string }) {
  const [done, setDone] = useState(false);
  return (
    <button type="button" className="btn sm ghost" aria-label={label} title={done ? "Copied" : "Copy"} onClick={() => {
      navigator.clipboard?.writeText(text).then(() => { setDone(true); setTimeout(() => setDone(false), 1500); }, () => {});
    }}>{done ? "Copied" : "Copy"}</button>
  );
}

function PreviewStrip({ project, env, open, admin }: { project: string; env: EnvView; open: number; admin: boolean }) {
  const [shown, setShown] = useState(false);
  const [err, setErr] = useState("");
  const copied = env.services.filter((s) => !s.no_preview), left = env.services.filter((s) => s.no_preview);
  return (
    <div className="col" style={{ marginTop: 16 }}>
      <div className="card strip">
        <div className="row" style={{ gap: 8 }}>
          <span className="muted">Each pull request gets a copy of</span>
          {copied.map((s) => <span key={s.service} className="pill on">{s.service}</span>)}
          {left.length > 0 && <span className="muted">· not copied</span>}
          {left.map((s) => <span key={s.service} className="pill off">{s.service}</span>)}
        </div>
        <div className="row">
          {admin && <button type="button" className="btn sm" aria-expanded={shown} onClick={() => setShown(!shown)}>Preview settings</button>}
          <Link to={`/p/${project}/${env.name}/pulls`} className="btn sm">{open === 1 ? "1 open pull request" : `${open} open pull requests`}</Link>
        </div>
      </div>
      {shown && (
        <div className="card pad col">
          <h2>What a pull request copies</h2>
          {err && <Callout tone="bad">{err}</Callout>}
          {env.services.map((s) => (
            <label key={s.service} className="row" style={{ gap: 10 }}>
              <input type="checkbox" checked={!s.no_preview} onChange={async (ev) => {
                setErr("");
                try { await api("PUT", `/api/services/${project}/${env.name}/${s.service}/preview`, { on: ev.target.checked }); } catch (x) { setErr(errText(x)); }
              }} />
              <b>{s.service}</b><span className="dim">{KIND[s.kind]?.[0] ?? s.kind}</span>
            </label>
          ))}
          <p className="dim">Off is <span className="mono">preview: false</span> in yawl.yaml: a worker with a bot token, or a slow build, stays out. It applies to previews made from now.</p>
          <PreviewSenders project={project} />
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
    <div className="col" style={{ marginTop: 16 }}>
      <div className="seg" role="group" aria-label="Pull requests">
        <button type="button" className={which === "open" ? "on" : ""} aria-pressed={which === "open"} onClick={() => setWhich("open")}>{`Open · ${open.length}`}</button>
        <button type="button" className={which === "merged" ? "on" : ""} aria-pressed={which === "merged"} onClick={() => setWhich("merged")}>{`Merged, last day · ${merged.length}`}</button>
      </div>
      {err && <Callout tone="bad">{err}</Callout>}
      {list.length === 0 ? (
        <Empty text={which === "open" ? "No open pull requests. One opened on a repository this environment builds gets its preview here in about a minute." : "Nothing merged in the last day."} />
      ) : (
        <div className="card prs">
          {list.map((r) => <PullCard key={r.id} project={project} r={r} onClose={async () => {
            setErr("");
            try { await api("POST", `/api/previews/${r.id}/close`); } catch (e) { setErr(errText(e)); }
            load.reload();
          }} />)}
        </div>
      )}
    </div>
  );
}

const STEP: Record<string, string> = { build: "Build", deploy: "Deploy", live: "Live" };

function PullCard({ project, r, onClose }: { project: string; r: PreviewRow; onClose: () => void }) {
  const logs = r.env && r.service && r.deploy_id ? `/p/${project}/${r.env}/s/${r.service}/deploys/${r.deploy_id}` : "";
  const running = r.steps?.some((s) => s.state === "run"), failed = r.steps?.some((s) => s.state === "fail") || r.state === "failed";
  const sha = r.commit || r.head;
  return (
    <article className="pr">
      <div className="col" style={{ gap: 6, minWidth: 0 }}>
        <div className="row" style={{ gap: 10 }}><span className="num dim">#{r.number}</span><b>{r.title}</b></div>
        <div className="row mono muted" style={{ gap: 8, fontSize: 13 }}>
          <span>{r.branch}</span>{r.base && <><span aria-hidden="true">→</span><span>{r.base}</span></>}{r.author && <span className="dim">· {r.author}</span>}
        </div>
        {r.landed ? (
          <table className="tbl landed"><tbody>
            {r.landed.map(([k, v]) => <tr key={k}><td className="mono dim">{k}</td><td>{v}</td></tr>)}
          </tbody></table>
        ) : r.steps ? (
          <ol className="steps" aria-label="Steps">
            {r.steps.map((s) => <li key={s.name} className={s.state}><i aria-hidden="true">{s.state === "done" ? "✓" : s.state === "fail" ? "✕" : s.state === "run" ? "…" : ""}</i>{STEP[s.name] ?? s.name}{s.ms ? " " + took(s.ms) : ""}<span className="sr">{`: ${s.state}`}</span></li>)}
          </ol>
        ) : r.comment ? <span className="muted">{r.comment}</span> : null}
        {r.cause && <span className="bad-text">{r.cause}</span>}
        <div className="row dim" style={{ gap: 14, fontSize: 13 }}>
          {sha && <span className="num">{shortSha(sha)}</span>}
          {r.at > 0 && <span>{r.merged ? `merged ${ago(r.at)}` : `pushed ${ago(r.at)}`}</span>}
          {r.state === "live" && !r.merged && <span>removed after 7 days without a push</span>}
        </div>
      </div>
      <div className="row acts">
        {r.url && !r.merged && <a className="btn pri" href={r.url} target="_blank" rel="noreferrer">Open preview ↗</a>}
        {!r.url && logs && running && <Link className="btn pri" to={logs}>Watch build</Link>}
        {!r.url && logs && failed && !running && <Link className="btn pri" to={logs}>See why</Link>}
        {r.url && logs && !r.merged && <Link className="btn" to={logs}>Logs</Link>}
        {CLOSABLE.includes(r.state) && !r.merged && <button type="button" className="btn ghost" aria-label={`Close preview ${r.number}`} onClick={onClose}>Close</button>}
      </div>
    </article>
  );
}

function PreviewSenders({ project }: { project: string }) {
  const got = useLoad<{ senders: string[] }>(`/api/projects/${project}/preview-senders`);
  const [text, setText] = useState<string | null>(null);
  const [msg, setMsg] = useState("");
  const value = text ?? (got.data?.senders ?? []).join(" ");
  return (
    <form className="row" onSubmit={async (ev) => {
      ev.preventDefault();
      setMsg("");
      try {
        await api("PUT", `/api/projects/${project}/preview-senders`, { senders: value.split(/[\s,]+/).filter(Boolean) });
        setText(null);
        got.reload();
        setMsg("Saved.");
      } catch (e) { setMsg(errText(e)); }
    }}>
      <Field label="Who may build previews" hint="Forge logins, like octocat or dependabot[bot]. A pull request or push from anyone else is held. Empty: anyone who may push to the repository.">
        {(id) => <input id={id} className="in mono" value={value} onChange={(e) => setText(e.target.value)} />}
      </Field>
      <button className="btn sm" type="submit">Save</button>
      {msg && <span className="muted">{msg}</span>}
    </form>
  );
}
