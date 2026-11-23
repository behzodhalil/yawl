import { useState } from "react";
import { api } from "../api";
import { changeLine } from "../format";
import { navigate } from "../router";
import { setRef, setVar } from "../seal";
import { ALWAYS_BACKED_UP, DB_SHOWN, DB_VERSIONS, dbOptions, emptyForm, isDb, nameFromRepo, planLines, releaseLines, volumeError } from "../serviceForm";
import { Shell } from "../Shell";
import type { Change, InspectData, ProjectView, ServiceForm, Volume } from "../types";
import { Callout, EnvName, Field, Icon, Link, errText, useBusy, useLoad } from "../ui";

const KINDS: [string, string, string, string][] = [
  ["web", "Web Service", "globe", "Answers HTTP on a URL. Deploys blue/green behind a health check."],
  ["worker", "Background Worker", "worker", "Runs without a port: queues, schedulers, bots."],
  ["cron", "Cron Job", "cron", "Runs a command on a schedule, on the server, even when the panel is down."],
  ["postgres", "Postgres", "postgres", "A database with daily backups off this server."],
  ["valkey", "Valkey", "valkey", "A Redis compatible cache and queue, kept on disk."],
  ["mysql", "MySQL", "mysql", "MySQL 8.4 with daily backups off this server."],
  ["mongodb", "MongoDB", "mongodb", "MongoDB 8.0 as a replica set, with daily backups."],
  ["mariadb", "MariaDB", "mariadb", "MariaDB 11.4 with daily backups off this server."],
  ["clickhouse", "ClickHouse", "clickhouse", "ClickHouse 25.8 for analytics."],
];

type KeyEntry = { value: string; ref: string; build: boolean };

export type FieldsPart = "source" | "runtime" | "resources";

export function ServiceFields({ form, set, image = !!form.image, part }: { form: ServiceForm; set: (f: ServiceForm) => void; image?: boolean; part?: FieldsPart }) {
  const up = (p: Partial<ServiceForm>) => set({ ...form, ...p });
  const shows = (p: FieldsPart) => !part || part === p;
  return (
    <>
      {shows("source") && <SourceFields form={form} up={up} image={image} />}
      {shows("runtime") && <RuntimeFields form={form} up={up} />}
      {shows("resources") && (
        <div className="grid2">
          <CPUField cpu={form.cpu} set={(cpu) => up({ cpu })} />
          <Field label="Memory" hint="e.g. 512 MB">{(id) => <input id={id} className="in mono" placeholder="No limit" value={form.memory} onChange={(e) => up({ memory: e.target.value })} />}</Field>
        </div>
      )}
    </>
  );
}

function CPUField({ cpu, set }: { cpu: number; set: (cpu: number) => void }) {
  const [text, setText] = useState(() => (cpu ? String(cpu) : ""));
  return (
    <Field label="CPU" hint="e.g. 0.5 cores">{(id) => <input id={id} className="in mono" inputMode="decimal" placeholder="No limit" value={text} onChange={(e) => {
      const t = e.target.value;
      const n = Number(t.trim().replace(",", "."));
      setText(t);
      set(t.trim() === "" ? 0 : Number.isFinite(n) ? n : -1);
    }} />}</Field>
  );
}

type Up = (p: Partial<ServiceForm>) => void;

function SourceFields({ form, up, image }: { form: ServiceForm; up: Up; image: boolean }) {
  if (isDb(form.kind)) {
    return (
      <Field label="Version">{(id) => (
        <select id={id} className="in" value={form.version} onChange={(e) => up({ version: Number(e.target.value) })}>
          {DB_VERSIONS[form.kind].map((v) => <option key={v} value={v}>{DB_SHOWN[form.kind] ?? v}</option>)}
        </select>
      )}</Field>
    );
  }
  if (image) return null;
  return <Field label="Branch" hint="A push to it deploys.">{(id) => <input id={id} className="in mono" value={form.branch} onChange={(e) => up({ branch: e.target.value })} />}</Field>;
}

function RuntimeFields({ form, up }: { form: ServiceForm; up: Up }) {
  if (isDb(form.kind)) {
    const always = ALWAYS_BACKED_UP.includes(form.kind);
    return (
      <>
        {!always && (
          <label className="row" style={{ gap: 6 }}>
            <input type="checkbox" checked={form.backup} onChange={(e) => up({ backup: e.target.checked })} />Back up every night
          </label>
        )}
        {(always || form.backup) && (
          <Field label="Daily backup (UTC)" hint="HH:MM. Backups need storage, set in Settings.">{(id) => (
            <input id={id} className="in mono" value={form.backup_at} onChange={(e) => up({ backup_at: e.target.value })} pattern="([01][0-9]|2[0-3]):[0-5][0-9]" />
          )}</Field>
        )}
      </>
    );
  }
  if (form.kind === "cron") {
    return (
      <>
        <div className="grid2">
          <Field label="Schedule" hint="Five fields: minute hour day month weekday. 0 3 * * * is 03:00 every day.">{(id) => <input id={id} className="in mono" placeholder="0 3 * * *" value={form.schedule} onChange={(e) => up({ schedule: e.target.value })} />}</Field>
          <Field label="Time zone" hint="Empty: the panel's own. For example Asia/Tashkent.">{(id) => <input id={id} className="in mono" value={form.timezone} onChange={(e) => up({ timezone: e.target.value })} />}</Field>
        </div>
        <Field label="Command" hint="Runs in a new container of the latest image, with this environment's variables. One argument per line, never a shell string.">
          {(id) => <textarea id={id} className="in mono" rows={3} style={{ height: "auto", padding: 8 }} placeholder={"node\njobs/report.js"} value={form.command.join("\n")} onChange={(e) => up({ command: e.target.value.split("\n") })} />}
        </Field>
        <Field label="Time limit (minutes)" hint="A run past it is stopped and counts as failed.">{(id) => (
          <input id={id} className="in mono" type="number" min={1} max={1440} step="any" value={form.timeout / 60} onChange={(e) => up({ timeout: Math.round(Number(e.target.value) * 60) })} />
        )}</Field>
      </>
    );
  }
  return (
    <>
      {form.kind === "web" && (
        <div className="grid2">
          <Field label="Port" hint="Yawl sends it as PORT.">{(id) => <input id={id} className="in mono" type="number" min={1} max={65535} value={form.port} onChange={(e) => up({ port: Number(e.target.value) })} />}</Field>
          <Field label="Health path" hint="Must answer below 500.">{(id) => <input id={id} className="in mono" value={form.health_path} onChange={(e) => up({ health_path: e.target.value })} />}</Field>
        </div>
      )}
      <Field label="Release command" hint="Runs on the new version before it starts, after a database snapshot. One argument per line.">
        {(id) => <textarea id={id} className="in mono" rows={3} style={{ height: "auto", padding: 8 }} value={form.release.join("\n")} onChange={(e) => up({ release: e.target.value.split("\n") })} />}
      </Field>
      <VolumesField volumes={form.volumes ?? []} set={(volumes) => up({ volumes })} />
    </>
  );
}

function VolumesField({ volumes, set }: { volumes: Volume[]; set: (v: Volume[]) => void }) {
  const [added, setAdded] = useState(-1);
  const put = (i: number, p: Partial<Volume>) => set(volumes.map((v, j) => (j === i ? { ...v, ...p } : v)));
  return (
    <div className="field" role="group" aria-label="Volumes">
      <span data-u="w5">Volumes</span>
      {volumes.map((v, i) => {
        const err = volumeError(v);
        return (
          <div key={i} className="col" data-u="g4">
            <div className="row" style={{ gap: 8 }}>
              <input className="in mono" style={{ flex: "1 1 140px", width: "auto" }} aria-label={`Name, volume ${i + 1}`} placeholder="Name" autoFocus={i === added}
                aria-invalid={!!err} value={v.name} onChange={(e) => put(i, { name: e.target.value })} />
              <input className="in mono" style={{ flex: "2 1 200px", width: "auto" }} aria-label={`Path, volume ${i + 1}`} placeholder="Path"
                aria-invalid={!!err} value={v.path} onChange={(e) => put(i, { path: e.target.value })} />
              <button type="button" className="btn sm ghost" aria-label={`Remove volume ${i + 1}`} onClick={() => set(volumes.filter((_, j) => j !== i))}>Remove</button>
            </div>
            {err && <span className="bad-text">{err}</span>}
          </div>
        );
      })}
      <div><button type="button" className="btn sm" onClick={() => (setAdded(volumes.length), set([...volumes, { name: "", path: "" }]))}>+ Add volume</button></div>
      <span className="hint">A directory kept across deploys and backed up every night, like uploads at /app/uploads. A deploy then stops the old version before the new one starts.</span>
    </div>
  );
}

export function cleanForm(f: ServiceForm): ServiceForm {
  return {
    ...f, release: releaseLines(f.release.join("\n")), domains: f.domains.map((d) => d.trim()).filter(Boolean), command: releaseLines(f.command.join("\n")),
    volumes: (f.volumes ?? []).map((v) => ({ name: v.name.trim(), path: v.path.trim() })).filter((v) => v.name || v.path),
  };
}

export function PlanBox({ changes }: { changes: Change[] }) {
  return (
    <div className="card pad col" style={{ gap: 4 }}>
      <h3>Plan</h3>
      {changes.map((c, i) => <code key={i} className={c.op === "unchanged" ? "dim" : ""}>{changeLine(c)}</code>)}
    </div>
  );
}

export function NewService({ project, env }: { project: string; env: string }) {
  const tree = useLoad<ProjectView[]>("/api/projects");
  const envServices = tree.data?.find((p) => p.name === project)?.envs.find((e) => e.name === env)?.services ?? [];
  const [form, setFormRaw] = useState<ServiceForm | null>(null);
  const [inspected, setInspected] = useState<InspectData | null>(null);
  const [reading, setReading] = useState(false);
  const [source, setSource] = useState<"repo" | "image">("repo");
  const [importing, setImporting] = useState(false);
  const [keys, setKeys] = useState<Record<string, KeyEntry>>({});
  const [plan, setPlan] = useState<Change[] | null>(null);
  const [err, setErr] = useState("");
  const [busy, run] = useBusy();
  const [made, setMade] = useState("");
  const envPath = `/api/environments/${project}/${env}/services`;
  const setForm = (f: ServiceForm) => (setFormRaw(f), setPlan(null));

  function from(s: "repo" | "image") {
    setSource(s);
    setInspected(null);
    setKeys({});
    if (form) setForm({ ...form, repo: "", branch: s === "image" ? "" : "main", image: "", auto_deploy: s === "repo" });
  }

  function choose(kind: string, imported = false) {
    setImporting(imported);
    setSource("repo");
    setForm(emptyForm(kind));
    setInspected(null);
    setKeys({});
    setPlan(null);
    setErr("");
  }

  async function read() {
    if (!form) return;
    setReading(true);
    setErr("");
    try {
      const d = await api<InspectData>("POST", "/api/inspect", { project, env, repo: form.repo, branch: form.branch });
      setInspected(d);
      setForm({ ...form, name: form.name || nameFromRepo(form.repo), health_path: form.kind === "cron" ? "" : d.health_path, port: form.kind === "web" && d.port ? d.port : form.port });
      setKeys(Object.fromEntries(d.missing.map((k) => [k, { value: "", ref: "", build: /^(VITE_|NEXT_PUBLIC_)/.test(k) }]))); // baked into the page by the build
    } catch (e) {
      setErr(errText(e));
    } finally {
      setReading(false);
    }
  }

  async function showPlan() {
    if (!form) return;
    setErr("");
    try {
      setPlan((await api<{ changes: Change[] }>("POST", envPath + "?new=1&dry=1", cleanForm(form))).changes);
    } catch (e) {
      setErr(errText(e));
    }
  }

  async function apply() {
    if (!form) return;
    setErr("");
    await run(async () => {
      try {
        const f = cleanForm(form);
        await api("POST", envPath + "?new=1", f);
        setMade(f.name);
        const svc = `/api/services/${project}/${env}/${f.name}`;
        for (const [k, e] of Object.entries(keys)) {
          if (e.ref) await setRef(svc, k, e.ref);
          else if (e.value) await setVar(svc, k, e.value, e.build);
        }
        const d = await api<{ deploy_id: string }>("POST", svc + "/deploys", { commit: "" });
        navigate(`/p/${project}/${env}/s/${f.name}/deploys/${d.deploy_id}`);
      } catch (e) {
        setErr(errText(e));
      }
    });
  }

  const fromImage = !!form && !isDb(form.kind) && source === "image";
  const needsRepo = form && !isDb(form.kind) && !fromImage;
  const ready = form && (!needsRepo || inspected);
  return (
    <Shell crumb={[<Link to="/">Projects</Link>, <Link to={`/p/${project}/${env}`}>{project}</Link>, <EnvName env={env} />, "New service"]} active="/">
      <h1>{importing ? "Import an app" : "New service"}</h1>
      <div className="grid3">
        {KINDS.map(([k, label, icon, text]) => (
          <button key={k} type="button" className={"card tpl" + (form?.kind === k && !importing ? " on" : "")} onClick={() => choose(k)}>
            <span className="row"><Icon name={icon} /><b>{label}</b></span>
            <span className="muted" style={{ textAlign: "left" }}>{text}</span>
          </button>
        ))}
        <button type="button" className={"card tpl" + (importing ? " on" : "")} onClick={() => choose("web", true)}>
          <span className="row"><Icon name="code" /><b>Import an app</b></span>
          <span className="muted" style={{ textAlign: "left" }}>Made with Lovable, Bolt, v0 or Replit, from its GitHub repository.</span>
          <span className="row marks"><Icon name="lovable" /><Icon name="bolt" /><Icon name="v0" /><Icon name="replit" /></span>
        </button>
        <Link to={`/p/${project}/${env}/templates`} className="card tpl">
          <span className="row"><Icon name="template" /><b>From a template</b></span>
          <span className="muted" style={{ textAlign: "left" }}>Uptime Kuma, Grafana and Loki, Umami, n8n: ready to install.</span>
        </Link>
      </div>

      {form && !isDb(form.kind) && !importing && (
        <div className="seg" role="group" aria-label="Source">
          <button type="button" className={source === "repo" ? "on" : ""} aria-pressed={source === "repo"} onClick={() => from("repo")}>From a Git repository</button>
          <button type="button" className={source === "image" ? "on" : ""} aria-pressed={source === "image"} onClick={() => from("image")}>From an image</button>
        </div>
      )}

      {fromImage && form && (
        <div className="card pad col w-form">
          <h2>Image</h2>
          <Field label="Image" hint="Pulled only when you deploy. A private registry needs a login in Integrations, Registries.">
            {(id) => <input id={id} className="in mono" placeholder="grafana/grafana:11.6" value={form.image}
              onChange={(e) => setForm({ ...form, image: e.target.value, name: !form.name || form.name === nameFromImage(form.image) ? nameFromImage(e.target.value) : form.name })} />}
          </Field>
        </div>
      )}

      {needsRepo && (
        <div className="card pad col w-form">
          <h2>Repository</h2>
          <Field label="Repository" hint="A GitHub URL. Private repositories need the GitHub App, in Integrations.">
            {(id) => <input id={id} className="in mono" placeholder="https://github.com/acme/api" value={form.repo} onChange={(e) => setForm({ ...form, repo: e.target.value })} />}
          </Field>
          <Field label="Branch">{(id) => <input id={id} className="in mono" value={form.branch} onChange={(e) => setForm({ ...form, branch: e.target.value })} />}</Field>
          <div><button className="btn" type="button" disabled={reading || !form.repo} onClick={read}>{reading ? "Reading the repository…" : "Read repository"}</button></div>
          {inspected && (
            <div className="col" style={{ gap: 4 }}>
              {planLines(inspected, form.kind).map((l) => <span key={l} className="row" style={{ gap: 8 }}><Icon name="check" />{l}</span>)}
              {inspected.warnings.map((w) => <Callout key={w} tone="warn">{w}</Callout>)}
              {inspected.notes.map((n) => <Callout key={n} tone="">{n}</Callout>)}
            </div>
          )}
        </div>
      )}

      {ready && form && (
        <div className="card pad col w-form">
          <h2>Settings</h2>
          <Field label="Name" hint="Lowercase letters, digits and dashes.">{(id) => <input id={id} className="in" value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} />}</Field>
          <ServiceFields form={form} set={setForm} image={fromImage} />
          {Object.keys(keys).length > 0 && (
            <div className="col">
              <h3>Missing keys</h3>
              <p className="muted">Sealed in this browser before they leave it. Leave one empty to set it later.</p>
              {Object.entries(keys).map(([k, e]) => {
                const put = (p: Partial<KeyEntry>) => setKeys({ ...keys, [k]: { ...e, ...p } });
                return (
                  <div key={k} className="row" style={{ alignItems: "flex-end" }}>
                    <code style={{ minWidth: 160 }}>{k}</code>
                    {dbOptions(k, envServices).length > 0 && (
                      <select aria-label={k} className="in" style={{ width: "auto" }} value={e.ref ? "ref:" + e.ref : ""} onChange={(ev) => put({ ref: ev.target.value.replace(/^ref:/, "") })}>
                        <option value="">Type a value</option>
                        {dbOptions(k, envServices).map((d) => <option key={d} value={`ref:${d}.url`}>{`Reference a database: ${d}.url`}</option>)}
                      </select>
                    )}
                    {!e.ref && (
                      <>
                        <input aria-label={`${k} value`} className="in mono" type="password" autoComplete="off" style={{ flex: 1, minWidth: 160 }} value={e.value} onChange={(ev) => put({ value: ev.target.value })} />
                        <label className="row" style={{ gap: 6 }}><input type="checkbox" checked={e.build} onChange={(ev) => put({ build: ev.target.checked })} />Build-time</label>
                      </>
                    )}
                  </div>
                );
              })}
            </div>
          )}
          {plan && <PlanBox changes={plan} />}
          <div className="row">
            {made ? null : !plan ? <button className="btn pri" type="button" onClick={showPlan}>Show the plan</button>
              : <button className="btn pri" type="button" disabled={busy} onClick={apply}>Apply and deploy</button>}
          </div>
        </div>
      )}
      {err && <Callout tone="bad">{err}{made && <> <Link to={`/p/${project}/${env}/s/${made}`}>{`Open ${made}`}</Link></>}</Callout>}
    </Shell>
  );
}

export const nameFromImage = (ref: string) => nameFromRepo((ref.split("@")[0].split("/").pop() ?? "").split(":")[0]);
