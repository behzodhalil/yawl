import { useEffect, useRef, useState, type RefObject } from "react";
import { api } from "../../api";
import { navigate } from "../../router";
import type { Change, DomainView, ServiceForm, ServiceView } from "../../types";
import { Callout, Confirm, Field, Loading, errText, useLoad, useUnsaved } from "../../ui";
import { PlanBox, ServiceFields, cleanForm } from "../NewService";
import { DeleteDialog } from "../../DeleteDialog";
import { DB_SHOWN, isDb } from "../../serviceForm";

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
  source: ["branch", "version", "builder", "root"],
  runtime: ["port", "health_path", "release", "volumes", "backup", "backup_at", "schedule", "timezone", "command", "timeout"],
  resources: ["cpu", "memory", "replicas"],
  domains: ["domains", "www", "noindex"],
};
const part = (f: ServiceForm, keys: (keyof ServiceForm)[]) => {
  const c = cleanForm(f);
  return JSON.stringify(Object.fromEntries(keys.map((k) => [k, c[k]])), blank);
};
const parts = (...p: unknown[]) => p.filter(Boolean).join(" · ");

function summary(id: string, f: ServiceForm, v: ServiceView) {
  const replicas = f.replicas ?? 1;
  if (id === "source") return isDb(f.kind) ? `${f.kind} ${DB_SHOWN[f.kind] ?? f.version}` : parts(f.repo.replace(/^(\w+:\/\/)?[^/]*\//, ""), f.branch);
  if (id === "runtime") return parts(f.port > 0 && `:${f.port}`, f.health_path, f.schedule);
  if (id === "resources") return parts(f.memory, f.cpu > 0 && `${f.cpu} CPU`, `${replicas} ${replicas === 1 ? "replica" : "replicas"}`);
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
    if (ready) document.getElementById(location.hash.slice(1))?.scrollIntoView?.();
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
  const body = useRef<HTMLDivElement>(null);
  const save = useFormSave(project, env, () => (setSaved(true), view.reload()));
  useEffect(() => { if (view.data) setFormRaw(view.data.form); }, [view.data]);
  const setForm = (f: ServiceForm) => (setFormRaw(f), save.reset(), setSaved(false));
  const dirty = !!view.data && !!form && !same(form, view.data.form);
  useUnsaved(dirty);
  const [on, go] = useSpy(body, !!view.data && !!form);
  if (!view.data || !form) return <Loading what={view} />;
  const data = view.data;
  const was = data.form;
  const web = form.kind === "web";
  const hasSource = isDb(form.kind) || !form.image;
  const builds = !!form.repo && !form.image && ["web", "worker", "cron"].includes(form.kind);
  const sections = ([["source", "Source", hasSource], ["runtime", "Runtime", true], ["resources", "Resources", true], ["domains", "Domains", web],
    ["access", "Access", web]] as const).filter(([, , has]) => has);
  const changed = sections.filter(([id]) => PARTS[id] && part(form, PARTS[id]) !== part(was, PARTS[id])).map(([, title]) => title);
  const unready = view.data.domains.some((d) => d.dns_state !== "ok" || d.cert_state !== "issued");
  const current = on || sections[0][0];
  return (
    <div className="settings">
      <label className="jump">Jump to
        <select className="in" value={current} onChange={(e) => (go(e.target.value), document.getElementById(e.target.value)?.scrollIntoView?.())}>
          {sections.map(([id, title]) => <option key={id} value={id}>{title}</option>)}
          <option value="danger">Delete service</option>
        </select>
      </label>
      <nav aria-label="On this page" className="rail">
        {sections.map(([id, title]) => (
          <a key={id} href={`#${id}`} aria-current={current === id ? "location" : undefined} onClick={() => go(id)}>
            <span className="t">{title}</span>
            <span className="s">{summary(id, form, data)}</span>
            {changed.includes(title) ? <span className="flag edit">Edited</span>
              : id === "domains" && unready && <span className="flag warn" role="img" aria-label="A domain is not ready yet" title="A domain is not ready yet" />}
          </a>
        ))}
        {dirty ? (
          <div className="savebox" aria-live="polite">
            <p>{changed.length ? `${changed.length} ${changed.length === 1 ? "section" : "sections"} changed · ${changed.join(", ")}` : "Unsaved changes"}</p>
            {save.plan && <PlanBox changes={save.plan} />}
            {save.err && <Callout tone="bad">{save.err}</Callout>}
            <div className="row">
              {!save.plan ? <button className="btn pri sm" onClick={() => save.show(form)}>Show the plan</button>
                : <button className="btn pri sm" onClick={() => save.apply(form)}>Apply</button>}
              <button className="btn ghost sm" onClick={() => (setForm(was), setDiscards((n) => n + 1))}>Discard</button>
            </div>
          </div>
        ) : saved && <div className="savebox" aria-live="polite"><Callout tone="ok">Saved. It takes effect on the next deploy.</Callout></div>}
        <a href="#danger" className="del" aria-current={current === "danger" ? "location" : undefined} onClick={() => go("danger")}>Delete service</a>
      </nav>
      <div className="col" ref={body}>
        {hasSource && (
          <section id="source" className="card pad col">
            <h2>Source</h2>
            <ServiceFields form={form} set={setForm} part="source" />
            {builds && (
              <div className="row">
                <span className="muted">Builder</span>
                <span>{form.builder === "railpack" ? "Railpack" : "Its Dockerfile; else Yawl's builder for Go, Node and Python; else Railpack"}</span>
                <button className="btn sm" onClick={() => setForm({ ...form, builder: form.builder === "railpack" ? "" : "railpack" })}>
                  {form.builder === "railpack" ? "Choose the builder automatically" : "Build with Railpack"}
                </button>
              </div>
            )}
            {builds && (
              <Field label="Folder" hint="The folder of the repository to build, like landing or apps/web, with its own Dockerfile. Empty builds the whole repository.">
                {(id) => <input id={id} className="in mono" placeholder="the whole repository" value={form.root ?? ""} onChange={(e) => setForm({ ...form, root: e.target.value.trim() })} />}
              </Field>
            )}
          </section>
        )}
        <section id="runtime" className="card pad col">
          <h2>Runtime</h2>
          <ServiceFields form={form} set={setForm} part="runtime" />
        </section>
        <section id="resources" className="card pad col">
          <h2>Resources</h2>
          <ServiceFields key={discards} form={form} set={setForm} part="resources" />
        </section>
        {web && (
          <section id="domains" className="card pad col">
            <h2>Domains</h2>
            <DomainRows domains={form.domains} states={view.data.domains} set={(domains) => setForm({ ...form, domains })} />
            <div className="grid2">
              <Field label="www">{(id) => (
                <select id={id} className="in" value={form.www ?? ""} onChange={(e) => setForm({ ...form, www: e.target.value })}>
                  <option value="">Both names serve</option>
                  <option value="bare">www.name sends to name</option>
                  <option value="www">name sends to www.name</option>
                </select>
              )}</Field>
              <Field label="Search engines">{(id) => (
                <select id={id} className="in" value={form.noindex ? "noindex" : ""} onChange={(e) => setForm({ ...form, noindex: e.target.value === "noindex" })}>
                  <option value="">May index</option>
                  <option value="noindex">noindex</option>
                </select>
              )}</Field>
            </div>
          </section>
        )}
        {web && <Access path={svcPath} user={view.data.password_user} maintenance={!!view.data.maintenance} reload={view.reload} />}
        <DeleteService project={project} env={env} service={service} path={svcPath} kind={form.kind} />
      </div>
    </div>
  );
}

function StateMark({ label, state, ok, bad }: { label: string; state: string; ok: string; bad?: string }) {
  const text = `${label} ${state}`;
  if (state === ok) return <span className="tag">{text}</span>;
  if (state === bad) return <span className="tag bad">{text}</span>;
  return <span className="muted">{text}</span>;
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
          <div key={i} className="col" data-u="g4">
            <div className="row" style={{ gap: 8 }}>
              <input ref={i === 0 ? first : undefined} className="in mono" style={{ flex: "1 1 220px", width: "auto" }} aria-label={`Domain ${i + 1}`} placeholder="acme.uz"
                autoFocus={i === added || (i === 0 && location.hash === "#domains")} value={host} onChange={(e) => set(rows.map((h, j) => (j === i ? e.target.value : h)))} />
              {d && <StateMark label="DNS" state={d.dns_state} ok="ok" bad="wrong" />}
              {d && <StateMark label="certificate" state={d.cert_state} ok="issued" />}
              {!empty && (
                <button type="button" className="btn sm ghost" aria-label={`Remove ${host.trim() || `domain ${i + 1}`}`} onClick={() => set(rows.filter((_, j) => j !== i))}>Remove</button>
              )}
            </div>
            {d && d.dns_state !== "ok" && d.dns_detail && <span className="word warn">{d.dns_detail}</span>}
          </div>
        );
      })}
      <div><button type="button" className="btn sm" onClick={() => (empty ? first.current?.focus() : (setAdded(rows.length), set([...rows, ""])))}>Add domain</button></div>
      <span className="hint">A host, or a host and a path like acme.uz/api. Point an A record at the server first; the certificate is asked for once DNS is right.</span>
    </div>
  );
}

function DeleteService({ project, env, service, path, kind }: { project: string; env: string; service: string; path: string; kind: string }) {
  const [info, setInfo] = useState<{ purge_at: number; users: string[] } | null>(null);
  const [err, setErr] = useState("");
  return (
    <section id="danger" className="card pad col">
      <h2>Delete service</h2>
      <p className="muted">{kind === "postgres" ? "The database stops at once. After 48 hours in the trash its data and its backups are gone." : "It stops at once and waits 48 hours in the trash, where it can be restored."}</p>
      <div><button className="btn danger" onClick={async () => {
        setErr("");
        try { setInfo(await api<{ purge_at: number; users: string[] }>("GET", path + "/delete-info")); } catch (e) { setErr(errText(e)); }
      }}>Delete service</button></div>
      {err && <Callout tone="bad">{err}</Callout>}
      {info && <DeleteDialog what={service} path={path} purgeAt={info.purge_at} users={info.users} usesRef={`${service}.url`}
        onClose={() => setInfo(null)} onDone={() => navigate(`/p/${project}/${env}`)} />}
    </section>
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
    <section id="access" className="card pad col">
      <h2>Access</h2>
      {user ? (
        <div className="row"><span>{`Asks for a password: user ${user}.`}</span><button className="btn ghost" onClick={() => setAsk("password")}>Remove the password</button></div>
      ) : (
        <>
          <div className="grid2">
            <Field label="User">{(id) => <input id={id} className="in" autoComplete="off" value={u} onChange={(e) => setU(e.target.value)} />}</Field>
            <Field label="Password">{(id) => <input id={id} className="in" type="password" autoComplete="new-password" value={pw} onChange={(e) => setPw(e.target.value)} />}</Field>
          </div>
          <div><button className="btn" disabled={!u || !pw} onClick={() => put("password", { user: u, password: pw })}>Set the password</button></div>
        </>
      )}
      <div>
        {maintenance
          ? <button className="btn pri" onClick={() => put("maintenance", { on: false })}>Back to the app</button>
          : <button className="btn" onClick={() => setAsk("maintenance")}>Show the maintenance page</button>}
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
    </section>
  );
}
