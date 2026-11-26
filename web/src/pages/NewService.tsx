import { useState } from "react";
import { Badge, Button, Checkbox, Combobox, Grid, LayerCard, Loader, Select, Switch, Tabs, Textarea, cn, linkVariants } from "@cloudflare/kumo";
import { api } from "../api";
import { ago, changeLine } from "../format";
import { navigate } from "../router";
import { setRef, setVar } from "../seal";
import { ALWAYS_BACKED_UP, DB_SHOWN, DB_VERSIONS, dbOptions, emptyForm, isDb, nameFromRepo, planLines, releaseLines, volumeError } from "../serviceForm";
import { Shell } from "../Shell";
import type { Change, InspectData, ProjectView, ServiceForm, Volume } from "../types";
import { Callout, EnvName, Field, Icon, Link, errText, useBusy, useLoad, useReveal, Input, Card } from "../ui";
import { BuildStep, choiceFor, detectedChoice, type BuildChoice } from "./BuildStep";

const KINDS: [string, string, string, string][] = [
  ["web", "Web Service", "globe", "Answers HTTP on a URL. Deploys blue/green behind a health check."],
  ["worker", "Background Worker", "worker", "Runs without a port: queues, schedulers, bots."],
  ["cron", "Cron Job", "cron", "Runs a command on a schedule, on the server, even when the panel is down."],
  ["postgres", "Postgres", "postgres", `Postgres ${DB_VERSIONS.postgres[0]} with daily backups off this server.`],
  ["valkey", "Valkey", "valkey", "A Redis compatible cache and queue, kept on disk."],
  ["mysql", "MySQL", "mysql", "MySQL 8.4 with daily backups off this server."],
  ["mongodb", "MongoDB", "mongodb", "MongoDB 8.0 as a replica set, with daily backups."],
  ["mariadb", "MariaDB", "mariadb", "MariaDB 11.4 with daily backups off this server."],
  ["clickhouse", "ClickHouse", "clickhouse", "ClickHouse 25.8 for analytics."],
];

const TEMPLATES_NAMED = 4;

export function templatesLine(list: { title: string }[] | undefined): string {
  if (!list?.length) return "Ready stacks, installed in one step.";
  const named = list.slice(0, TEMPLATES_NAMED).map((t) => t.title).join(", ");
  const more = list.length - TEMPLATES_NAMED;
  return more > 0 ? `${named} and ${more} more, ready to install.` : `${named}: ready to install.`;
}

const TILE = "flex cursor-pointer flex-col gap-1.5 p-4 text-left hover:ring-kumo-line";

const STEPS = ["Source", "Build", "Domain and resources"];

function Steps({ step, form }: { step: number; form: ServiceForm }) {
  const repo = form.repo.replace(/^https?:\/\/[^/]+\//, "").replace(/\.git$/, "");
  return (
    <ol aria-label="Steps" className="flex flex-wrap items-center gap-2 text-sm">
      {STEPS.map((s, i) => (
        <li key={s} aria-current={step === i + 1 ? "step" : undefined}
          className={cn("flex items-center gap-2 rounded-xs px-2.5 py-1.5 ring-1 ring-kumo-line", step === i + 1 ? "bg-kumo-base font-semibold text-kumo-default" : "text-kumo-subtle")}>
          <span className="grid size-5 place-items-center rounded-xs bg-kumo-recessed font-mono text-xs">{step > i + 1 ? <Icon name="check" /> : i + 1}</span>
          {s}
          {i === 0 && step > 1 && <span className="font-mono font-normal">{`${repo} · ${form.branch}`}</span>}
        </li>
      ))}
    </ol>
  );
}

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
        <div className="grid grid-cols-[repeat(auto-fit,minmax(320px,1fr))] gap-4">
          <CPUField cpu={form.cpu} set={(cpu) => up({ cpu })} />
          <Field label="Memory" hint="like 512 MB">{(id) => <Input id={id} className="font-mono" placeholder="No limit" value={form.memory} onChange={(e) => up({ memory: e.target.value })} />}</Field>
        </div>
      )}
    </>
  );
}

function CPUField({ cpu, set }: { cpu: number; set: (cpu: number) => void }) {
  const [text, setText] = useState(() => (cpu ? String(cpu) : ""));
  return (
    <Field label="CPU" hint="like 0.5 cores">{(id) => <Input id={id} className="font-mono" inputMode="decimal" placeholder="No limit" value={text} onChange={(e) => {
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
        <Select aria-labelledby={id + "-label"} value={String(form.version)} onValueChange={(v) => up({ version: Number(v) })}
          items={Object.fromEntries(DB_VERSIONS[form.kind].map((v) => [String(v), String(DB_SHOWN[form.kind] ?? v)]))} />
      )}</Field>
    );
  }
  if (image) return null;
  return <Field label="Branch" hint="A push to it deploys.">{(id) => <Input id={id} className="font-mono" value={form.branch} onChange={(e) => up({ branch: e.target.value })} />}</Field>;
}

function RuntimeFields({ form, up }: { form: ServiceForm; up: Up }) {
  if (isDb(form.kind)) {
    const always = ALWAYS_BACKED_UP.includes(form.kind);
    return (
      <>
        {!always && (
          <Switch label="Back up every night" checked={form.backup} onCheckedChange={(on) => up({ backup: on })} />
        )}
        {(always || form.backup) && (
          <Field label="Daily backup (UTC)" hint="HH:MM. Backups need storage, set in Settings.">{(id) => (
            <Input id={id} className="font-mono" value={form.backup_at} onChange={(e) => up({ backup_at: e.target.value })} pattern="([01][0-9]|2[0-3]):[0-5][0-9]" />
          )}</Field>
        )}
      </>
    );
  }
  if (form.kind === "cron") {
    return (
      <>
        <div className="grid grid-cols-[repeat(auto-fit,minmax(320px,1fr))] gap-4">
          <Field label="Schedule" hint="Five fields: minute hour day month weekday. 0 3 * * * is 03:00 every day.">{(id) => <Input id={id} className="font-mono" placeholder="0 3 * * *" value={form.schedule} onChange={(e) => up({ schedule: e.target.value })} />}</Field>
          <TimezoneField value={form.timezone} set={(timezone) => up({ timezone })} />
        </div>
        <Field label="Command" hint="Runs in a new container of the latest image, with this environment's variables. One argument per line, never a shell string.">
          {(id) => <Textarea id={id} className="font-mono" rows={3} placeholder={"node\njobs/report.js"} value={form.command.join("\n")} onChange={(e) => up({ command: e.target.value.split("\n") })} />}
        </Field>
        <Field label="Time limit (minutes)" hint="A run past it is stopped and counts as failed.">{(id) => (
          <Input id={id} className="font-mono" type="number" min={1} max={1440} step="any" value={form.timeout / 60} onChange={(e) => up({ timeout: Math.round(Number(e.target.value) * 60) })} />
        )}</Field>
      </>
    );
  }
  return (
    <>
      {form.kind === "web" && (
        <div className="grid grid-cols-[repeat(auto-fit,minmax(320px,1fr))] gap-4">
          <Field label="Port" hint="Yawl sends it as PORT.">{(id) => <Input id={id} className="font-mono" type="number" min={1} max={65535} value={form.port} onChange={(e) => up({ port: Number(e.target.value) })} />}</Field>
          <Field label="Health path" hint="Must answer below 500.">{(id) => <Input id={id} className="font-mono" value={form.health_path} onChange={(e) => up({ health_path: e.target.value })} />}</Field>
        </div>
      )}
      <Field label="Release command" hint="Runs on the new version before it starts, after a database snapshot. One argument per line.">
        {(id) => <Textarea id={id} className="font-mono" rows={3} value={form.release.join("\n")} onChange={(e) => up({ release: e.target.value.split("\n") })} />}
      </Field>
      <VolumesField volumes={form.volumes ?? []} set={(volumes) => up({ volumes })} />
    </>
  );
}

const ZONES = Intl.supportedValuesOf("timeZone");

function TimezoneField({ value, set }: { value: string; set: (zone: string) => void }) {
  return (
    <Combobox label="Time zone" description="Empty: the panel's own." items={ZONES} value={value || null} onValueChange={(v) => set((v as string | null) ?? "")}>
      <Combobox.TriggerInput className="font-mono" placeholder="The panel's own" />
      <Combobox.Content>
        <Combobox.Empty>No such time zone.</Combobox.Empty>
        <Combobox.List>{(z: string) => <Combobox.Item key={z} value={z}>{z}</Combobox.Item>}</Combobox.List>
      </Combobox.Content>
    </Combobox>
  );
}

function VolumesField({ volumes, set }: { volumes: Volume[]; set: (v: Volume[]) => void }) {
  const [added, setAdded] = useState(-1);
  const put = (i: number, p: Partial<Volume>) => set(volumes.map((v, j) => (j === i ? { ...v, ...p } : v)));
  return (
    <div className="flex flex-col gap-1.5" role="group" aria-label="Volumes">
      <span className="font-medium">Volumes</span>
      {volumes.map((v, i) => {
        const err = volumeError(v);
        return (
          <div key={i} className="flex flex-col gap-1">
            <div className="flex flex-wrap items-center gap-2">
              <Input className="w-auto flex-[1_1_140px] font-mono" aria-label={`Name, volume ${i + 1}`} placeholder="Name" autoFocus={i === added}
                aria-invalid={!!err} aria-describedby={err ? `volume-err-${i}` : undefined} value={v.name} onChange={(e) => put(i, { name: e.target.value })} />
              <Input className="w-auto flex-[2_1_200px] font-mono" aria-label={`Path, volume ${i + 1}`} placeholder="Path"
                aria-invalid={!!err} aria-describedby={err ? `volume-err-${i}` : undefined} value={v.path} onChange={(e) => put(i, { path: e.target.value })} />
              <Button size="sm" variant="ghost" aria-label={`Remove volume ${i + 1}`} onClick={() => set(volumes.filter((_, j) => j !== i))}>Remove</Button>
            </div>
            {err && <span id={`volume-err-${i}`} className="text-kumo-danger">{err}</span>}
          </div>
        );
      })}
      <div><Button size="sm" onClick={() => (setAdded(volumes.length), set([...volumes, { name: "", path: "" }]))}>Add volume</Button></div>
      <span className="text-sm text-kumo-subtle">A directory kept across deploys and backed up every night, like uploads at /app/uploads. A deploy then stops the old version before the new one starts.</span>
    </div>
  );
}

export function cleanForm(f: ServiceForm): ServiceForm {
  return {
    ...f, steps: f.steps && Object.fromEntries(Object.entries(f.steps).map(([k, v]) => [k, v?.trim() ?? ""])), release: releaseLines(f.release.join("\n")), domains: f.domains.map((d) => d.trim()).filter(Boolean), command: releaseLines(f.command.join("\n")),
    volumes: (f.volumes ?? []).map((v) => ({ name: v.name.trim(), path: v.path.trim() })).filter((v) => v.name || v.path),
  };
}

export function PlanBox({ changes }: { changes: Change[] }) {
  return (
    <Card title="Plan" level={3}>
      <div className="flex flex-col gap-1">{changes.map((c, i) => <code key={i} className={c.op === "unchanged" ? "text-kumo-subtle" : undefined}>{changeLine(c)}</code>)}</div>
    </Card>
  );
}

export function NewService({ project, env, kind }: { project: string; env: string; kind?: string }) {
  const tree = useLoad<ProjectView[]>("/api/projects");
  const templates = useLoad<{ title: string }[]>("/api/templates");
  const envServices = tree.data?.find((p) => p.name === project)?.envs.find((e) => e.name === env)?.services ?? [];
  const [form, setFormRaw] = useState<ServiceForm | null>(() => (kind && KINDS.some(([k]) => k === kind) ? emptyForm(kind) : null));
  const [inspected, setInspected] = useState<InspectData | null>(null);
  const [reading, setReading] = useState(false);
  const [source, setSource] = useState<"repo" | "image">("repo");
  const [importing, setImporting] = useState(false);
  const [keys, setKeys] = useState<Record<string, KeyEntry>>({});
  const [plan, setPlan] = useState<Change[] | null>(null);
  const [err, setErr] = useState("");
  const [busy, run] = useBusy();
  const [made, setMade] = useState("");
  const [step, setStep] = useState(1);
  const [choice, setChoice] = useState<BuildChoice>(() => choiceFor(""));
  const envPath = `/api/environments/${project}/${env}/services`;
  const setForm = (f: ServiceForm) => (setFormRaw(f), setPlan(null));
  const reveal = useReveal<HTMLDivElement>(importing ? "import" : form?.kind);

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
    setStep(1);
  }

  async function read() {
    if (!form) return;
    setReading(true);
    setErr("");
    try {
      const d = await api<InspectData>("POST", "/api/inspect", { project, env, repo: form.repo, branch: form.branch });
      setInspected(d);
      setChoice(detectedChoice(d));
      setForm({ ...form, name: form.name || nameFromRepo(form.repo), health_path: form.kind === "cron" ? "" : d.health_path, port: form.kind === "web" && d.port ? d.port : form.port });
      setKeys(Object.fromEntries(d.missing.map((k) => [k, { value: "", ref: "", build: /^(VITE_|NEXT_PUBLIC_)/.test(k) }])));
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
  const wizard = !!needsRepo && form.kind === "web" && !importing;
  const on = (n: number) => !wizard || step === n;
  return (
    <Shell crumb={[<Link to="/">Projects</Link>, <Link to={`/p/${project}/${env}`}>{project}</Link>, <EnvName env={env} />, "New service"]} active="/">
      <h1>{importing ? "Import an app" : "New service"}</h1>
      <Grid variant="3up" gap="sm">
        {KINDS.map(([k, label, icon, text]) => (
          <LayerCard key={k} render={<button type="button" onClick={() => choose(k)} />} aria-pressed={form?.kind === k && !importing} className={cn(TILE, form?.kind === k && !importing && "bg-kumo-tint ring-2 ring-kumo-brand")}>
            <span className="flex items-center gap-2.5"><Icon name={icon} /><b className="font-semibold">{label}</b></span>
            <span className="text-kumo-subtle">{text}</span>
          </LayerCard>
        ))}
        <LayerCard render={<button type="button" onClick={() => choose("web", true)} />} aria-pressed={importing} className={cn(TILE, importing && "bg-kumo-tint ring-2 ring-kumo-brand")}>
          <span className="flex items-center gap-2.5"><Icon name="code" /><b className="font-semibold">Import an app</b></span>
          <span className="text-kumo-subtle">Made with Lovable, Bolt, v0 or Replit, from its GitHub repository.</span>
          <span className="mt-0.5 flex items-center gap-2 [&_.ico]:size-4.5"><Icon name="lovable" /><Icon name="bolt" /><Icon name="v0" /><Icon name="replit" /></span>
        </LayerCard>
        <LayerCard render={<Link to={`/p/${project}/${env}/templates`} />} className={TILE}>
          <span className="flex items-center gap-2.5"><Icon name="template" /><b className="font-semibold">From a template</b></span>
          <span className="text-kumo-subtle">{templatesLine(templates.data)}</span>
        </LayerCard>
      </Grid>

      <div ref={reveal} className="flex flex-col gap-6">
      {form && !isDb(form.kind) && !importing && (
        <Tabs variant="segmented" className="self-start" aria-label="Source" value={source} onValueChange={(v) => from(v as "repo" | "image")}
          tabs={[{ value: "repo", label: "From a Git repository" }, { value: "image", label: "From an image" }]} />
      )}

      {fromImage && form && (
        <Card title="Image" className="max-w-[var(--w-form)]">
          <Field label="Image" hint="Pulled only when you deploy. A private registry needs a login in Integrations, Registries.">
            {(id) => <Input id={id} className="font-mono" placeholder="grafana/grafana:11.6" value={form.image}
              onChange={(e) => setForm({ ...form, image: e.target.value, name: !form.name || form.name === nameFromImage(form.image) ? nameFromImage(e.target.value) : form.name })} />}
          </Field>
        </Card>
      )}

      {wizard && <Steps step={step} form={form} />}

      {needsRepo && on(1) && (
        <Card title="Repository" className="max-w-[var(--w-form)]">
          <RepoPicker chosen={form.repo} onPick={(r) => setForm({ ...form, repo: r.url, branch: !form.branch || form.branch === "main" ? r.default_branch : form.branch })} />
          <Field label="Repository" hint="A GitHub URL. Private repositories need the GitHub App, in Integrations.">
            {(id) => <Input id={id} className="font-mono" placeholder="https://github.com/acme/api" value={form.repo} onChange={(e) => setForm({ ...form, repo: e.target.value })} />}
          </Field>
          <Field label="Branch">{(id) => <Input id={id} className="font-mono" value={form.branch} onChange={(e) => setForm({ ...form, branch: e.target.value })} />}</Field>
          <div className="flex items-center gap-2.5"><Button disabled={reading || !form.repo} onClick={read}>{reading ? "Reading the repository…" : "Read repository"}</Button>{reading && <Loader size="sm" />}</div>
          {inspected && (
            <div className="flex flex-col gap-1">
              {planLines(inspected, form.kind).map((l) => <span key={l.text} className={cn("flex items-center gap-2", l.kind === "warn" && "text-kumo-warning")}><Icon name={l.kind === "warn" ? "log-alert" : "check"} />{l.text}</span>)}
              {inspected.warnings.map((w) => <Callout key={w} tone="warn">{w}</Callout>)}
              {inspected.notes.map((n) => <Callout key={n} tone="">{n}</Callout>)}
            </div>
          )}
          {wizard && <div><Button variant="primary" disabled={!inspected} onClick={() => setStep(2)}>Continue</Button></div>}
        </Card>
      )}

      {wizard && step === 2 && inspected && form && (
        <>
          <BuildStep form={form} set={setForm} inspected={inspected} choice={choice} setChoice={setChoice} />
          <div className="flex gap-2.5"><Button onClick={() => setStep(1)}>Back</Button><Button variant="primary" onClick={() => setStep(3)}>Continue</Button></div>
        </>
      )}

      {ready && form && on(3) && (
        <Card title="Settings" className="max-w-[var(--w-form)]">
          <Field label="Name" hint="Starts with a letter; lowercase letters, digits and dashes, up to 31.">{(id) => <Input id={id} value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} />}</Field>
          {isDb(form.kind) && <ServiceFields form={form} set={setForm} part="source" />}
          <ServiceFields form={form} set={setForm} part="runtime" />
          <ServiceFields form={form} set={setForm} part="resources" />
          {Object.keys(keys).length > 0 && (
            <div className="flex flex-col gap-3">
              <h3>Missing keys</h3>
              <p className="text-kumo-subtle">Sealed in this browser before they leave it. Leave one empty to set it later.</p>
              {Object.entries(keys).map(([k, e]) => {
                const put = (p: Partial<KeyEntry>) => setKeys({ ...keys, [k]: { ...e, ...p } });
                return (
                  <div key={k} className="flex flex-wrap items-end gap-2.5">
                    <code className="min-w-40">{k}</code>
                    {dbOptions(k, envServices).length > 0 && (
                      <Select aria-label={k} value={e.ref ? "ref:" + e.ref : ""} onValueChange={(v) => put({ ref: String(v).replace(/^ref:/, "") })}
                        items={{ "": "Type a value", ...Object.fromEntries(dbOptions(k, envServices).map((d) => [`ref:${d}.url`, `Reference a database: ${d}.url`])) }} />
                    )}
                    {!e.ref && (
                      <>
                        <Input aria-label={`${k} value`} className="min-w-40 flex-1 font-mono" type="password" autoComplete="off" value={e.value} onChange={(ev) => put({ value: ev.target.value })} />
                        <Checkbox label="Build-time" checked={e.build} onCheckedChange={(on) => put({ build: !!on })} />
                      </>
                    )}
                  </div>
                );
              })}
            </div>
          )}
          {plan && <PlanBox changes={plan} />}
          {err && <Callout tone="bad">{err}{made && <> <Link to={`/p/${project}/${env}/s/${made}`} className={linkVariants({ variant: "inline" })}>{`Open ${made}`}</Link></>}</Callout>}
          <div className="flex flex-wrap items-center gap-2.5">
            {wizard && !made && <Button onClick={() => setStep(2)}>Back</Button>}
            {made ? null : !plan ? <Button variant="primary" disabled={!SERVICE_NAME.test(form.name)} onClick={showPlan}>Show the plan</Button>
              : <Button variant="primary" disabled={busy} onClick={apply}>Apply and deploy</Button>}
          </div>
        </Card>
      )}
      {err && !(ready && form && on(3)) && <Callout tone="bad">{err}</Callout>}
      </div>
    </Shell>
  );
}

const SERVICE_NAME = /^[a-z][a-z0-9-]{0,30}$/;

export const nameFromImage = (ref: string) => nameFromRepo((ref.split("@")[0].split("/").pop() ?? "").split(":")[0]);

type Repo = { full_name: string; url: string; private: boolean; pushed_at: number; default_branch: string };

function RepoPicker({ chosen, onPick }: { chosen: string; onPick: (r: Repo) => void }) {
  const repos = useLoad<Repo[]>("/api/github/repos");
  if (repos.error) return <p className="text-kumo-subtle">{repos.error}</p>;
  if (!repos.data) return null;
  if (repos.data.length === 0) {
    return <p className="text-kumo-subtle"><Link to="/integrations" className={linkVariants({ variant: "inline" })}>Connect the GitHub App in Integrations</Link> to pick from your repositories.</p>;
  }
  return (
    <Combobox label="Your repositories" description="From the GitHub App, newest push first." items={repos.data} value={repos.data.find((r) => r.url === chosen) ?? null}
      itemToStringLabel={(r: Repo) => r.full_name} isItemEqualToValue={(a: Repo, b: Repo) => a.url === b.url}
      onValueChange={(r) => { if (r) onPick(r as Repo); }}>
      <Combobox.TriggerInput className="font-mono" placeholder="Search your repositories" />
      <Combobox.Content>
        <Combobox.Empty>No repository matches.</Combobox.Empty>
        <Combobox.List>
          {(r: Repo) => (
            <Combobox.Item key={r.url} value={r}>
              <span className="flex w-full items-center gap-2">
                <span className="min-w-0 flex-1 truncate font-mono text-sm">{r.full_name}</span>
                {r.private && <Badge variant="neutral">Private</Badge>}
                <span className="shrink-0 text-sm text-kumo-subtle">{r.pushed_at ? `pushed ${ago(r.pushed_at)}` : "never pushed"}</span>
              </span>
            </Combobox.Item>
          )}
        </Combobox.List>
      </Combobox.Content>
    </Combobox>
  );
}
