import { useEffect, useState } from "react";
import { api } from "../../api";
import { CopyTo } from "./CopyTo";
import { navigate } from "../../router";
import type { Change, DomainView, ServiceForm, ServiceView } from "../../types";
import { Callout, Confirm, Field, Loading, Modal, errText, useBusy, useLoad, useUnsaved } from "../../ui";
import { PlanBox, ServiceFields, cleanForm } from "../NewService";
import { DeleteDialog } from "../../DeleteDialog";
import { isDb } from "../../serviceForm";

export function useFormSave(project: string, env: string, onDone: () => void) {
  const [plan, setPlan] = useState<Change[] | null>(null);
  const [err, setErr] = useState("");
  const path = `/api/environments/${project}/${env}/services`;
  return {
    plan, err, reset: () => setPlan(null),
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
const same = (a: ServiceForm, b: ServiceForm) => JSON.stringify(a, blank) === JSON.stringify(b, blank);

export function ServiceSettings({ project, env, service }: { project: string; env: string; service: string }) {
  const svcPath = `/api/services/${project}/${env}/${service}`;
  const view = useLoad<ServiceView>(svcPath);
  const [form, setFormRaw] = useState<ServiceForm | null>(null);
  const [saved, setSaved] = useState(false);
  const [rolling, setRolling] = useState(false);
  const [rbErr, setRbErr] = useState("");
  const [, run] = useBusy();
  const save = useFormSave(project, env, () => (setSaved(true), view.reload()));
  useEffect(() => { if (view.data) setFormRaw(view.data.form); }, [view.data]);
  const setForm = (f: ServiceForm) => (setFormRaw(f), save.reset(), setSaved(false));
  const dirty = !!view.data && !!form && !same(form, view.data.form);
  useUnsaved(dirty);
  if (!view.data || !form) return <Loading what={view} />;
  const web = form.kind === "web";
  const hasSource = isDb(form.kind) || !form.image;
  const builds = !!form.repo && !form.image && ["web", "worker", "cron"].includes(form.kind);
  const sections = ([["source", "Source", hasSource], ["runtime", "Runtime", true], ["resources", "Resources", true], ["domains", "Domains", web],
    ["access", "Access", web], ["copy", "Copy / move", true], ["danger", "Danger zone", true]] as const).filter(([, , has]) => has);
  return (
    <div className="sections">
      <nav aria-label="Sections" className="col" data-u="g6">
        {sections.map(([id, title]) => <a key={id} href={`#${id}`} className="l">{title}</a>)}
      </nav>
      <div className="col">
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
          <ServiceFields form={form} set={setForm} part="resources" />
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
        {save.plan && <PlanBox changes={save.plan} />}
        {save.err && <Callout tone="bad">{save.err}</Callout>}
        {saved && <Callout tone="ok">Saved. It takes effect on the next deploy.</Callout>}
        <div className="row">
          {!save.plan ? <button className="btn pri" onClick={() => save.show(form)}>Show the plan</button>
            : <button className="btn pri" onClick={() => save.apply(form)}>Apply</button>}
          {dirty && <span className="muted">Unsaved changes</span>}
        </div>
        {web && <Access path={svcPath} user={view.data.password_user} maintenance={!!view.data.maintenance} reload={view.reload} />}
        <CopyTo project={project} env={env} service={service} />
        <section id="danger" className="card pad col">
          <h2>Danger zone</h2>
          <h3>Rollback</h3>
          <p className="muted">Put the previous version back. It starts, passes its health check, then takes the traffic.</p>
          <div><button className="btn" onClick={() => setRolling(true)}>Roll back</button></div>
          <DeleteService project={project} env={env} service={service} path={svcPath} kind={form.kind} />
        </section>
      </div>
      {rolling && (
        <Modal title={`Roll back ${service}?`} onClose={() => setRolling(false)}>
          <p>The database is not touched. Migrations that ran stay in place.</p>
          {rbErr && <Callout tone="bad">{rbErr}</Callout>}
          <div className="row">
            <button className="btn pri" onClick={() => run(async () => {
              try {
                const r = await api<{ deploy_id: string }>("POST", svcPath + "/rollback", {});
                navigate(`/p/${project}/${env}/s/${service}/deploys/${r.deploy_id}`);
              } catch (e) {
                setRbErr(errText(e));
              }
            })}>Roll back to the previous version</button>
            <button className="btn ghost" onClick={() => setRolling(false)}>Cancel</button>
          </div>
        </Modal>
      )}
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
  const rows = domains.length ? domains : [""];
  return (
    <div className="col">
      {rows.map((host, i) => {
        const d = states.find((s) => s.host === host.trim());
        return (
          <div key={i} className="col" data-u="g4">
            <div className="row" style={{ gap: 8 }}>
              <input className="in mono" style={{ flex: "1 1 220px", width: "auto" }} aria-label={`Domain ${i + 1}`} placeholder="acme.uz"
                autoFocus={i === added || (i === 0 && location.hash === "#domains")} value={host} onChange={(e) => set(rows.map((h, j) => (j === i ? e.target.value : h)))} />
              {d && <StateMark label="DNS" state={d.dns_state} ok="ok" bad="wrong" />}
              {d && <StateMark label="TLS" state={d.cert_state} ok="issued" />}
              {domains.length > 0 && (
                <button type="button" className="btn sm ghost" aria-label={`Remove ${host.trim() || `domain ${i + 1}`}`} onClick={() => set(rows.filter((_, j) => j !== i))}>Remove</button>
              )}
            </div>
            {d && d.dns_state !== "ok" && d.dns_detail && <span className="word warn">{d.dns_detail}</span>}
          </div>
        );
      })}
      <div><button type="button" className="btn sm" onClick={() => (setAdded(rows.length), set([...rows, ""]))}>+ Add domain</button></div>
      <p className="muted">A host, or a host and a path like acme.uz/api. Point an A record at the server first; the certificate is asked for once DNS is right.</p>
    </div>
  );
}

function DeleteService({ project, env, service, path, kind }: { project: string; env: string; service: string; path: string; kind: string }) {
  const [info, setInfo] = useState<{ purge_at: number; users: string[] } | null>(null);
  const [err, setErr] = useState("");
  return (
    <>
      <h3>Delete</h3>
      <p className="muted">{kind === "postgres" ? "The database stops at once. After 48 hours in the trash its data and its backups are gone." : "It stops at once and waits 48 hours in the trash, where it can be restored."}</p>
      <div><button className="btn danger" onClick={async () => {
        setErr("");
        try { setInfo(await api<{ purge_at: number; users: string[] }>("GET", path + "/delete-info")); } catch (e) { setErr(errText(e)); }
      }}>Delete service</button></div>
      {err && <Callout tone="bad">{err}</Callout>}
      {info && <DeleteDialog what={service} path={path} purgeAt={info.purge_at} users={info.users} usesRef={`${service}.url`}
        onClose={() => setInfo(null)} onDone={() => navigate(`/p/${project}/${env}`)} />}
    </>
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
