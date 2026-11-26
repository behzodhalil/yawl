import { useEffect, useRef, useState, type RefObject } from "react";
import { Badge, Button, LayerCard, Radio, Select, SensitiveInput, Switch, TableOfContents, TagInput, linkVariants } from "@cloudflare/kumo";
import { api } from "../../api";
import { spaced } from "../../format";
import { fragment, navigate } from "../../router";
import type { Change, DomainView, ProjectView, ServiceForm, ServiceView } from "../../types";
import { Callout, Confirm, Field, Link, Loading, errText, notify, useLoad, useUnsaved, Input } from "../../ui";
import { PlanBox, ServiceFields, cleanForm } from "../NewService";
import { DeleteDialog } from "../../DeleteDialog";
import { DB_SHOWN, isDb } from "../../serviceForm";
import { startDeploy } from "../../serviceActions";
import { UnsavedBar } from "../../unsaved";

export function useFormSave(project: string, env: string, onDone: () => void) {
  const [plan, setPlan] = useState<Change[] | null>(null);
  const [err, setErr] = useState("");
  const path = `/api/environments/${project}/${env}/services`;
  return {
    plan, err, reset: () => (setPlan(null), setErr("")),
    show: async (f: ServiceForm) => {
      setErr("");
      try { setPlan((await api<{ changes: Change[] }>("POST", path + "?dry=1", cleanForm(f))).changes); } catch (e) { setErr(errText(e)); }
    },
    apply: async (f: ServiceForm) => {
      setErr("");
      try { await api("POST", path, cleanForm(f)); setPlan(null); onDone(); } catch (e) { setErr(errText(e)); }
    },
  };
}

const blank = (_: string, v: unknown) => (v == null || v === "" || v === false || (Array.isArray(v) && v.every((x) => x === "")) ? undefined : v);
const same = (a: ServiceForm, b: ServiceForm) => JSON.stringify(cleanForm(a), blank) === JSON.stringify(cleanForm(b), blank);
const PARTS: Record<string, (keyof ServiceForm)[]> = {
  source: ["version", "builder", "root"],
  git: ["branch", "auto_deploy", "watch", "no_preview"],
  runtime: ["port", "health_path", "release", "volumes", "backup", "backup_at", "schedule", "timezone", "command", "timeout"],
  resources: ["cpu", "memory", "replicas"],
  domains: ["domains", "www", "noindex"],
};
const part = (f: ServiceForm, keys: (keyof ServiceForm)[]) => {
  const c = cleanForm(f);
  return JSON.stringify(Object.fromEntries(keys.map((k) => [k, c[k]])), blank);
};
const parts = (...p: unknown[]) => p.filter(Boolean).join(" · ");
const RAIL_ITEM = "grid grid-cols-[minmax(0,1fr)_auto] gap-x-2 rounded-sm border-0 px-2.5 py-2 whitespace-normal overflow-visible hover:font-normal [&>span]:contents";
const MAX_REPLICAS = 10;
const SECTION_LINK = linkVariants({ variant: "plain" }) + " text-sm";

function summary(id: string, f: ServiceForm, v: ServiceView) {
  const replicas = f.replicas ?? 1;
  if (id === "source") return isDb(f.kind) ? `${f.kind} ${DB_SHOWN[f.kind] ?? f.version}` : parts(f.repo.replace(/^(\w+:\/\/)?[^/]*\//, ""), f.branch);
  if (id === "git") return "push, PRs, checks";
  if (id === "runtime") return parts(f.port > 0 && `:${f.port}`, f.health_path, f.schedule);
  if (id === "resources") return parts(spaced(f.memory), f.cpu > 0 && `${f.cpu} CPU`, `${replicas} ${replicas === 1 ? "replica" : "replicas"}`);
  if (id === "domains") return f.domains.map((d) => d.trim()).find(Boolean) ?? "No custom domain";
  if (id === "access") return parts(v.password_user ? "Password" : "Public", v.maintenance && "maintenance");
  return "";
}

function useSpy(box: RefObject<HTMLDivElement | null>, ready: boolean) {
  const [on, setOn] = useState("");
  const hold = useRef(0);
  useEffect(() => {
    const cards = [...(box.current?.querySelectorAll<HTMLElement>("section[id]") ?? [])];
    const seen = new Map<string, boolean>();
    const spy = new IntersectionObserver((es) => {
      es.forEach((e) => seen.set(e.target.id, e.isIntersecting));
      const last = cards.filter((c) => seen.get(c.id)).at(-1);
      if (last && Date.now() > hold.current) setOn(last.id);
    }, { rootMargin: "0px 0px -60% 0px" });
    cards.forEach((c) => spy.observe(c));
    if (ready) document.getElementById(fragment())?.scrollIntoView?.();
    return () => spy.disconnect();
  }, [box, ready]);
  return [on, (id: string) => ((hold.current = Date.now() + 700), setOn(id))] as const;
}

export function ServiceSettings({ project, env, service }: { project: string; env: string; service: string }) {
  const svcPath = `/api/services/${project}/${env}/${service}`;
  const view = useLoad<ServiceView>(svcPath);
  const [form, setFormRaw] = useState<ServiceForm | null>(null);
  const [saved, setSaved] = useState(false);
  const [discards, setDiscards] = useState(0);
  const [typingWatch, setTypingWatch] = useState(false);
  const body = useRef<HTMLDivElement>(null);
  const [deployNow, setDeployNow] = useState(false);
  const [deployErr, setDeployErr] = useState("");
  const deployNext = useRef(false);
  const deploy = async () => {
    try { navigate(`/p/${project}/${env}/s/${service}/deploys/${await startDeploy(svcPath)}`); } catch (e) { setDeployErr(errText(e)); }
  };
  const save = useFormSave(project, env, () => {
    setSaved(true);
    view.reload();
    if (deployNext.current) deploy();
    else notify("Saved", "success", "It takes effect on the next deploy.");
  });
  useEffect(() => { if (view.data) setFormRaw(view.data.form); }, [view.data]);
  const setForm = (f: ServiceForm) => (setFormRaw(f), save.reset(), setSaved(false), setDeployErr(""));
  const dirty = !!view.data && !!form && (typingWatch || !same(form, view.data.form));
  useUnsaved(dirty);
  const [on, go] = useSpy(body, !!view.data && !!form);
  if (!view.data || !form) return <Loading what={view} />;
  const data = view.data;
  const was = data.form;
  const web = form.kind === "web";
  const builds = !!form.repo && !form.image && ["web", "worker", "cron"].includes(form.kind);
  const hasSource = isDb(form.kind) || builds;
  const sections = ([["source", "Source", hasSource], ["git", "Git automation", builds], ["runtime", "Runtime", true], ["resources", "Resources", true], ["domains", "Domains", web],
    ["access", "Access", web]] as const).filter(([, , has]) => has);
  const changed = sections.filter(([id]) => (id === "git" && typingWatch) || (PARTS[id] && part(form, PARTS[id]) !== part(was, PARTS[id]))).map(([, title]) => title);
  const unready = view.data.domains.some((d) => d.dns_state !== "ok" || d.cert_state !== "issued");
  const current = on || sections[0][0];
  const saveNow = (withDeploy: boolean) => {
    setDeployNow(withDeploy);
    if (!save.plan) return save.show(form);
    deployNext.current = withDeploy;
    return save.apply(form);
  };
  return (
    <>
    <div className="settings">
      <div className="jump">
        <span id="jump-to">Jump to</span>
        <Select aria-labelledby="jump-to" className="flex-1" value={current} onValueChange={(v) => (go(String(v)), document.getElementById(String(v))?.scrollIntoView?.())}
          items={[...sections.map(([id, title]) => ({ value: id, label: title })), { value: "danger", label: "Delete service" }]} />
      </div>
      <TableOfContents aria-label="On this page" className="rail">
        <TableOfContents.List className="gap-0.5 border-l-0">
          {sections.map(([id, title]) => (
            <TableOfContents.Item key={id} active={current === id} aria-current={current === id ? "location" : undefined} className={RAIL_ITEM}
              render={<a href={`#${id}`} onClick={() => go(id)} />}>
              <span className="t">{title}</span>
              <span className="s">{summary(id, form, data)}</span>
              {changed.includes(title) ? <span className="flag edit">Edited</span>
                : id === "git" ? <Badge variant="secondary" className="flag">New</Badge>
                : id === "domains" && unready && <span className="flag warn" role="img" aria-label="A domain is not ready yet" title="A domain is not ready yet" />}
            </TableOfContents.Item>
          ))}
        </TableOfContents.List>
        {!dirty && saved && deployErr && <Callout tone="bad">{`Saved, but the deploy did not start: ${deployErr}`}</Callout>}
        <a href="#danger" className="del" aria-current={current === "danger" ? "location" : undefined} onClick={() => go("danger")}>Delete service</a>
      </TableOfContents>
      <div className="flex flex-col gap-4 pb-[45vh] max-[760px]:pb-0" ref={body}>
        {hasSource && (
          <LayerCard id="source" render={<section />} className="flex flex-col">
            <h2>Source</h2>
            {isDb(form.kind) && <ServiceFields form={form} set={setForm} part="source" />}
            {builds && (
              <div className="field">
                <Radio.Group legend="Builder" value={form.builder === "railpack" ? "railpack" : ""} onValueChange={(v: string) => setForm({ ...form, builder: v })}
                  description="Automatic uses its Dockerfile; else Yawl's builder for Go, Node and Python; else Railpack.">
                  <Radio.Item label="Automatic" value="" />
                  <Radio.Item label="Railpack" value="railpack" />
                </Radio.Group>
              </div>
            )}
            {builds && (
              <Field label="Folder" hint="The folder of the repository to build, like landing or apps/web, with its own Dockerfile. Empty builds the whole repository.">
                {(id) => <Input id={id} className="font-mono" placeholder="the whole repository" value={form.root ?? ""} onChange={(e) => setForm({ ...form, root: e.target.value.trim() })} />}
              </Field>
            )}
          </LayerCard>
        )}
        {builds && <GitAutomation discards={discards} onTypingWatch={setTypingWatch} project={project} env={env} form={form} set={setForm} skipWords={data.skip_words ?? []} limits={{ max: data.preview_max, days: data.preview_idle_days }} />}
        <LayerCard id="runtime" render={<section />} className="flex flex-col">
          <h2>Runtime</h2>
          <ServiceFields form={form} set={setForm} part="runtime" />
        </LayerCard>
        <LayerCard id="resources" render={<section />} className="flex flex-col">
          <h2>Resources</h2>
          <ServiceFields key={discards} form={form} set={setForm} part="resources" />
          {(form.kind === "web" || form.kind === "worker") && (
            <Field label="Replicas" hint={form.volumes.length ? "One copy: a service with a volume cannot run more." : "Copies on the server, 1 to 10."}>
              {(id) => <Input id={id} type="number" className="w-24 font-mono" min={1} max={MAX_REPLICAS} step={1} value={form.replicas ?? 1} disabled={form.volumes.length > 0}
                onChange={(e) => setForm({ ...form, replicas: Math.min(MAX_REPLICAS, Math.max(1, Math.round(Number(e.target.value)) || 1)) })} />}
            </Field>
          )}
        </LayerCard>
        {web && (
          <LayerCard id="domains" render={<section />} className="flex flex-col">
            <h2>Domains</h2>
            <DomainRows domains={form.domains} states={view.data.domains} set={(domains) => setForm({ ...form, domains })} />
            <div className="field flex flex-col gap-4">
              <Radio.Group legend="www" value={form.www ?? ""} onValueChange={(v: string) => setForm({ ...form, www: v })}>
                <Radio.Item label="Both names serve" value="" />
                <Radio.Item label="www.name sends to name" value="bare" />
                <Radio.Item label="name sends to www.name" value="www" />
              </Radio.Group>
              <Switch label="Hide from search engines" labelTooltip="Sends noindex, so search engines leave the site out." checked={!!form.noindex}
                onClick={() => setForm({ ...form, noindex: !form.noindex })} />
            </div>
          </LayerCard>
        )}
        {web && <Access path={svcPath} user={view.data.password_user} maintenance={!!view.data.maintenance} reload={view.reload} />}
        <DeleteService project={project} env={env} service={service} path={svcPath} kind={form.kind} />
      </div>
    </div>
    {dirty && (
      <UnsavedBar detail={changed.length ? `${changed.length} ${changed.length === 1 ? "section" : "sections"} changed · ${changed.join(", ")}` : undefined}
        canDeploy={!isDb(form.kind) && !data.stopped} deployNow={deployNow}
        label={save.plan ? "Apply" : "Save"} deployLabel={save.plan ? "Apply and deploy" : "Save and deploy"}
        onReset={() => (setForm(was), setDiscards((n) => n + 1), setDeployNow(false), setTypingWatch(false))} onSave={saveNow}>
        {(save.plan || save.err) && (
          <div className="flex flex-col gap-2 rounded-sm bg-kumo-base p-2 shadow-lg ring ring-kumo-line">
            {save.plan && <PlanBox changes={save.plan} />}
            {save.err && <Callout tone="bad">{save.err}</Callout>}
          </div>
        )}
      </UnsavedBar>
    )}
    </>
  );
}

type PreviewRow = { number: number; state: string; source: string; comment?: string };
const PREVIEW_RUNNING = ["making", "live"];
const repoPath = (repo: string) => repo.replace(/^(\w+:\/\/)?/, "").replace(/\.git$/, "");

function GitAutomation({ project, env, form, set, skipWords, limits, discards, onTypingWatch }: {
  project: string; env: string; form: ServiceForm; set: (f: ServiceForm) => void; skipWords: string[]; limits: { max?: number; days?: number };
  discards: number; onTypingWatch: (typing: boolean) => void;
}) {
  const projects = useLoad<ProjectView[]>("/api/projects");
  const previews = useLoad<PreviewRow[]>(`/api/projects/${project}/previews`);
  const refused = useLoad<{ github: string; gitlab: string }>("/api/settings/commit-status");
  const envRow = projects.data?.find((p) => p.name === project)?.envs.find((e) => e.name === env);
  const mine = (previews.data ?? []).filter((p) => p.source === env);
  const running = mine.filter((p) => PREVIEW_RUNNING.includes(p.state)).length;
  const sample = mine.find((p) => p.comment);
  const refusedBy = refused.data && (refused.data.github || refused.data.gitlab);
  return (
    <LayerCard id="git" render={<section />} className="flex flex-col">
      <h2>Git automation</h2>
      <p className="text-kumo-subtle">From <a className={linkVariants({ variant: "inline" })} href={form.repo} target="_blank" rel="noreferrer">{repoPath(form.repo)}</a></p>
      <Field label="Deploy on push" hint="A push to the branch deploys the service.">
        {(id) => <Switch id={id} aria-labelledby={id + "-label"} checked={form.auto_deploy} onCheckedChange={(on) => set({ ...form, auto_deploy: on })} />}
      </Field>
      <Field label="Branch">{(id) => <Input id={id} className="font-mono" value={form.branch} onChange={(e) => set({ ...form, branch: e.target.value })} />}</Field>
      <Field label="Watch paths" hint="Deploy only when a changed file matches, like api/** or go.mod. Empty deploys every push. Press Enter or comma to add.">
        {(id) => <TagInput key={discards} id={id} aria-labelledby={id + "-label"} className="w-full max-w-md font-mono" placeholder="api/**" value={form.watch ?? []}
          onChange={(e) => onTypingWatch(e.target.value.trim() !== "")} onValueChange={(watch) => (onTypingWatch(false), set({ ...form, watch }))} />}
      </Field>
      {skipWords.length > 0 && (
        <Field label="Skip words" hint="A push whose commit message says one of these deploys nothing.">
          {(id) => <ul id={id} aria-labelledby={id + "-label"} className="flex flex-wrap gap-1.5">{skipWords.map((w) => <li key={w}><Badge variant="outline" className="font-mono">{w}</Badge></li>)}</ul>}
        </Field>
      )}
      <Field label="Pull request previews" hint={<>Every pull request gets its own copy, <code>pr-&lt;number&gt;</code>, with empty databases.{limits.max && limits.days ? ` At most ${limits.max} at once; gone when the pull request closes or after ${limits.days} days without a push.` : ""}</>}>
        {(id) => (
          <div id={id} className="flex flex-col gap-2">
            <div className="flex flex-wrap items-center gap-3">
              <Badge variant={form.no_preview ? "secondary" : "outline"}>{form.no_preview ? "Not copied into previews" : "Copied into previews"}</Badge>
              {previews.data && <span className="text-kumo-subtle">{running} running now</span>}
              <Link to={`/p/${project}/${env}#previews`} className={SECTION_LINK}>Choose what previews copy</Link>
            </div>
            {envRow && !envRow.preview && <span className="text-sm text-kumo-warning">This environment makes no previews. Turn them on from the environment menu.</span>}
          </div>
        )}
      </Field>
      <Field label="Comment on the pull request" hint="One comment on each pull request with a preview, kept up to date as its deploys finish.">
        {(id) => (
          <div id={id} className="flex flex-col gap-1.5">
            <span className="text-sm text-kumo-subtle">{sample ? `The comment on #${sample.number} now` : "No pull request from this environment yet."}</span>
            {sample && <code className="block rounded-sm bg-kumo-recessed px-3 py-2 font-mono text-sm ring ring-kumo-hairline">{sample.comment}</code>}
          </div>
        )}
      </Field>
      <Field label="Commit status" hint="A check on each commit: pending while it builds, then passed or failed.">
        {(id) => (
          <div id={id} className="flex flex-col gap-1.5 text-sm">
            {refusedBy
              ? <span className="text-kumo-warning">{`Refused: ${refusedBy}. Grant commit statuses: write.`}</span>
              : <span className="text-kumo-subtle">Set on every commit of a connected repository.</span>}
            <Link to="/integrations" className={SECTION_LINK}>Repository connections in Integrations</Link>
          </div>
        )}
      </Field>
    </LayerCard>
  );
}

function StateMark({ label, state, ok, bad }: { label: string; state: string; ok: string; bad?: string }) {
  const text = `${label} ${state}`;
  if (state === ok) return <Badge variant="success">{text}</Badge>;
  if (state === bad) return <Badge variant="error">{text}</Badge>;
  return <span className="text-kumo-subtle">{text}</span>;
}

function DomainRows({ domains, states, set }: { domains: string[]; states: DomainView[]; set: (d: string[]) => void }) {
  const [added, setAdded] = useState(-1);
  const first = useRef<HTMLInputElement>(null);
  const rows = domains.length ? domains : [""];
  const empty = rows.length === 1 && !rows[0].trim();
  return (
    <div className="field">
      {rows.map((host, i) => {
        const d = states.find((s) => s.host === host.trim());
        return (
          <div key={i} className="flex flex-col gap-1">
            <div className="flex flex-wrap items-center gap-2">
              <Input ref={i === 0 ? first : undefined} className="w-auto flex-[1_1_220px] font-mono" aria-label={`Domain ${i + 1}`} placeholder="acme.uz"
                autoFocus={i === added || (i === 0 && fragment() === "domains")} value={host} onChange={(e) => set(rows.map((h, j) => (j === i ? e.target.value : h)))} />
              {d && <StateMark label="DNS" state={d.dns_state} ok="ok" bad="wrong" />}
              {d && <StateMark label="Certificate" state={d.cert_state} ok="issued" />}
              {!empty && (
                <Button variant="ghost" size="sm" aria-label={`Remove ${host.trim() || `domain ${i + 1}`}`} onClick={() => set(rows.filter((_, j) => j !== i))}>Remove</Button>
              )}
            </div>
            {d && d.dns_state !== "ok" && d.dns_detail && <span className="text-kumo-warning">{d.dns_detail}</span>}
          </div>
        );
      })}
      <div className="mt-2"><Button size="sm" onClick={() => (empty ? first.current?.focus() : (setAdded(rows.length), set([...rows, ""])))}>Add domain</Button></div>
      <span className="text-sm text-kumo-subtle">A host, or a host and a path like acme.uz/api. Point an A record at the server first; the certificate is asked for once DNS is right.</span>
    </div>
  );
}

function DeleteService({ project, env, service, path, kind }: { project: string; env: string; service: string; path: string; kind: string }) {
  const [info, setInfo] = useState<{ purge_at: number; users: string[] } | null>(null);
  const [err, setErr] = useState("");
  return (
    <LayerCard id="danger" render={<section />} className="flex flex-col">
      <h2>Delete service</h2>
      <p className="text-kumo-subtle">{kind === "postgres" ? "The database stops at once. After 48 hours in the trash its data and its backups are gone." : "It stops at once and waits 48 hours in the trash, where it can be restored."}</p>
      <div><Button variant="secondary-destructive" onClick={async () => {
        setErr("");
        try { setInfo(await api<{ purge_at: number; users: string[] }>("GET", path + "/delete-info")); } catch (e) { setErr(errText(e)); }
      }}>Delete service</Button></div>
      {err && <Callout tone="bad">{err}</Callout>}
      {info && <DeleteDialog what={service} path={path} purgeAt={info.purge_at} users={info.users} usesRef={`${service}.url`}
        onClose={() => setInfo(null)} onDone={() => navigate(`/p/${project}/${env}`)} />}
    </LayerCard>
  );
}

function Access({ path, user, maintenance, reload }: { path: string; user?: string; maintenance: boolean; reload: () => void }) {
  const [u, setU] = useState("");
  const [pw, setPw] = useState("");
  const [err, setErr] = useState("");
  const [note, setNote] = useState("");
  const [ask, setAsk] = useState<"maintenance" | "password" | null>(null);
  const send = async (what: string, body: unknown) => {
    setErr("");
    setNote("");
    const r = await api<{ note?: string } | null>("PUT", `${path}/${what}`, body);
    setNote(r?.note ?? "");
    setPw("");
    reload();
  };
  const put = (what: string, body: unknown) => send(what, body).catch((e) => setErr(errText(e)));
  return (
    <LayerCard id="access" render={<section />} className="flex flex-col">
      <h2>Access</h2>
      {user ? (
        <div className="flex flex-wrap items-center gap-2.5"><span>{`Asks for a password: user ${user}.`}</span><Button variant="ghost" onClick={() => setAsk("password")}>Remove the password</Button></div>
      ) : (
        <>
          <>
            <Field label="User">{(id) => <Input id={id} className="w-full" autoComplete="off" value={u} onChange={(e) => setU(e.target.value)} />}</Field>
            <Field label="Password">{(id) => <SensitiveInput id={id} aria-labelledby={id + "-label"} className="w-full" autoComplete="new-password" value={pw} onValueChange={setPw} />}</Field>
          </>
          <div><Button disabled={!u || !pw} onClick={() => put("password", { user: u, password: pw })}>Set the password</Button></div>
        </>
      )}
      <div className="field">
        <Switch label="Maintenance page" labelTooltip="Visitors see the maintenance page instead of the app." checked={maintenance}
          onClick={() => (maintenance ? put("maintenance", { on: false }) : setAsk("maintenance"))} />
      </div>
      {note && <Callout tone="warn">{note}</Callout>}
      {err && <Callout tone="bad">{err}</Callout>}
      {ask === "maintenance" && (
        <Confirm title="Show the maintenance page?" yes="Yes, take it offline" onYes={() => send("maintenance", { on: true })} onClose={() => setAsk(null)}>
          <p>Visitors see the maintenance page until you turn it off.</p>
        </Confirm>
      )}
      {ask === "password" && (
        <Confirm title="Remove the password?" yes="Yes, remove it" onYes={() => send("password", { user: "", password: "" })} onClose={() => setAsk(null)}>
          <p>Anyone with the address can open the service.</p>
        </Confirm>
      )}
    </LayerCard>
  );
}
