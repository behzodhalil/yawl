import { isDb } from "../../serviceForm";
import { useContext, useEffect, useRef, useState, type FormEvent } from "react";
import { api } from "../../api";
import { randomKey, reveal, sealTo, setRef, setVar } from "../../seal";
import { startDeploy } from "../../serviceActions";
import { navigate } from "../../router";
import { Deploys } from "./Deploy";
import type { DiffRow, ProjectView, ServiceView } from "../../types";
import { Callout, Confirm, Field, Icon, Link, Loading, Menu, MenuItem, errText, useBusy, useLoad } from "../../ui";
import { stateKind } from "../../format";
import { Sources } from "./Sources";

type Entry = { key: string; scope: "service" | "shared" | "team" | "server"; service?: string; build?: boolean; ref?: string; updated_at: number };

function keySet(es: Entry[]) {
  return new Set(es.map((e) => (e.scope === "shared" ? e.key : `${e.service}/${e.key}`)));
}

const STATE: Record<DiffRow["state"], (env: string, other: string) => string> = {
  same: () => "same", different: () => "different", only_left: (e) => `only in ${e}`, only_right: (_, o) => `only in ${o}`,
};

function Diff({ project, env, other, service }: { project: string; env: string; other: string; service: string }) {
  const a = useLoad<Entry[]>(`/api/environments/${project}/${env}/env`);
  const b = useLoad<Entry[]>(`/api/environments/${project}/${other}/env`);
  const [rows, setRows] = useState<DiffRow[] | null>(null);
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);
  if (!a.data || !b.data) return null;
  const A = keySet(a.data), B = keySet(b.data);
  const lines = [...[...A].filter((k) => !B.has(k)).map((k) => `only in ${env}: ${k}`), ...[...B].filter((k) => !A.has(k)).map((k) => `only in ${other}: ${k}`)];
  async function compare() {
    setBusy(true);
    setErr("");
    try {
      const k = await randomKey();
      const keys: Record<string, string> = {};
      for (const e of [env, other]) {
        const { agent_pubkey } = await api<{ agent_pubkey: string }>("GET", `/api/environments/${project}/${e}/keys`);
        keys[e] = await sealTo(agent_pubkey, k);
      }
      const all = await api<DiffRow[]>("POST", `/api/projects/${project}/compare`, { left: env, right: other, keys });
      setRows(all.filter((r) => r.service === "" || r.service === service));
    } catch (e) {
      setErr(errText(e));
    } finally {
      setBusy(false);
    }
  }
  return (
    <div className="card pad col" style={{ gap: 4 }}>
      <h3>Compared with {other}</h3>
      {rows ? null : lines.length === 0 ? <span className="muted">Both have the same keys.</span>
        : lines.map((l) => <code key={l}>{l}</code>)}
      {rows ? rows.map((r) => <code key={r.service + "/" + r.key}>{`${r.key}: ${STATE[r.state](env, other)}`}</code>)
        : <div><button className="btn" disabled={busy} onClick={compare}>Compare values</button></div>}
      {err && <Callout tone="bad">{err}</Callout>}
    </div>
  );
}

export function Env({ project, env, service }: { project: string; env: string; service: string }) {
  const svcPath = `/api/services/${project}/${env}/${service}`;
  const envPath = `/api/environments/${project}/${env}`;
  const list = useLoad<Entry[]>(svcPath + "/env");
  const deploys = useContext(Deploys);
  const liveAt = deploys.data?.find((d) => d.live)?.created_at;
  const changed = (v: Entry) => liveAt !== undefined && v.updated_at > liveAt;
  const changes = list.data?.filter(changed).length ?? 0;
  const running = liveAt === undefined ? undefined : deploys.data?.find((d) => d.created_at > liveAt && stateKind(d.state) === "busy");
  const view = useLoad<ServiceView>(svcPath);
  const tree = useLoad<ProjectView[]>("/api/projects");
  const p = tree.data?.find((x) => x.name === project);
  const dbs = p?.envs.find((e) => e.name === env)?.services.filter((s) => isDb(s.kind)).map((s) => s.service) ?? [];
  const other = p?.envs.find((e) => e.name !== env)?.name;
  const [key, setKey] = useState("");
  const [value, setValue] = useState("");
  const [ref, setRefTo] = useState("");
  const [build, setBuild] = useState(false);
  const [shared, setShared] = useState(false);
  const [err, setErr] = useState("");
  const [shown, setShown] = useState<Record<string, string>>({});
  const [copied, setCopied] = useState("");
  const [filter, setFilter] = useState("");
  const [deploying, guardDeploy] = useBusy();
  const [del, setDel] = useState<Entry | null>(null);
  const [adding, setAdding] = useState(false);
  const [editing, setEditing] = useState(false);
  const [saving, save] = useBusy();
  const exists = !!list.data?.some((v) => v.key === key && v.scope === (shared ? "shared" : "service"));
  const timers = useRef<number[]>([]);
  const hideTimers = useRef(new Map<string, number>());
  useEffect(() => () => { timers.current.forEach(clearTimeout); hideTimers.current.forEach(clearTimeout); }, []);

  const reset = () => { setKey(""); setValue(""); setRefTo(""); setBuild(false); setShared(false); setEditing(false); };

  const add = (e: FormEvent) => { e.preventDefault(); return save(async () => {
    setErr("");
    try {
      const scope = shared ? envPath : svcPath;
      if (ref) await setRef(scope, key, ref);
      else await setVar(scope, key, value, build);
      reset(); setAdding(false);
      list.reload();
    } catch (e) {
      setErr(errText(e));
    }
  }); };

  function edit(v: Entry) {
    setKey(v.key); setValue(""); setRefTo(v.ref ?? ""); setBuild(!!v.build); setShared(v.scope === "shared");
    setEditing(true); setAdding(true);
  }

  const hide = (k: string) => {
    clearTimeout(hideTimers.current.get(k));
    hideTimers.current.delete(k);
    setShown((s) => { const n = { ...s }; delete n[k]; return n; });
  };

  async function show(k: string) {
    try {
      const v = await reveal(svcPath, k);
      setShown((s) => ({ ...s, [k]: v }));
      clearTimeout(hideTimers.current.get(k));
      hideTimers.current.set(k, window.setTimeout(() => hide(k), 30_000));
    } catch (e) {
      setErr(errText(e));
    }
  }

  async function copy(k: string) {
    setErr("");
    const value = reveal(svcPath, k);
    const written = ("ClipboardItem" in window && navigator.clipboard?.write
      ? navigator.clipboard.write([new ClipboardItem({ "text/plain": value.then((v) => new Blob([v], { type: "text/plain" })) })])
      : value.then((v) => navigator.clipboard.writeText(v))).then(() => true, () => false);
    try {
      await value;
    } catch (e) {
      setErr(errText(e));
      return;
    }
    if (!(await written)) {
      setErr("The browser did not allow copying. Use Show and select the value.");
      return;
    }
    setCopied(k);
    timers.current.push(window.setTimeout(() => setCopied((c) => (c === k ? "" : c)), 1500));
  }

  const deploy = () => guardDeploy(async () => {
    setErr("");
    try {
      navigate(`/p/${project}/${env}/s/${service}/deploys/${await startDeploy(svcPath)}`);
    } catch (e) {
      setErr(errText(e));
    }
  });

  const q = filter.trim().toLowerCase();
  const rows = list.data?.filter((v) => v.key.toLowerCase().includes(q)) ?? [];

  return (
    <div className="col">
      <div className="row" style={{ justifyContent: "space-between" }}>
        <p className="muted">Sealed in this browser; the server never reads them. They apply at the next deploy.</p>
        {!adding && <button className="btn" onClick={() => { reset(); setAdding(true); }}><Icon name="plus" />Add a variable</button>}
      </div>
      {err && <Callout tone="bad">{err}</Callout>}
      {changes > 0 && (
        <Callout tone="warn">
          <span className="row" style={{ justifyContent: "space-between" }}>
            {`${changes} change${changes === 1 ? "" : "s"} not deployed yet`}
            {running
              ? <Link to={`/p/${project}/${env}/s/${service}/deploys/${running.id}`} className="l">Deploying</Link>
              : <button className="btn sm pri" disabled={deploying} onClick={deploy}>Deploy</button>}
          </span>
        </Callout>
      )}
      {list.data && list.data.length > 0 && (
        <label className="tsearch"><Icon name="search" /><input type="search" aria-label="Filter variables" placeholder={`Filter ${list.data.length} ${list.data.length === 1 ? "variable" : "variables"}`} value={filter} onChange={(e) => setFilter(e.target.value)} /></label>
      )}
      {!list.data ? <Loading what={list} /> : (
        <div className="card scroll">
          <table className="tbl">
            <thead><tr><th>Key</th><th>Value</th><th /></tr></thead>
            <tbody>
              {list.data.length === 0 && <tr><td colSpan={3} className="muted">No variables yet.</td></tr>}
              {list.data.length > 0 && rows.length === 0 && <tr><td colSpan={3} className="muted">No variables match.</td></tr>}
              {rows.map((v) => (
                <tr key={v.scope + v.key}>
                  <td><code>{v.key}</code> {v.scope !== "service" && <span className="tag">{v.scope}</span>} {v.build && <span className="tag">build</span>} {changed(v) && <span className="tag">changed</span>}</td>
                  <td className="mono">{v.ref ? `→ ${v.ref}` : !(v.key in shown) ? "••••••••" : shown[v.key] === "" ? <span className="dim">empty</span> : shown[v.key]}</td>
                  <td>
                    {v.scope === "team" || v.scope === "server" ? (
                      <div className="row" style={{ justifyContent: "flex-end" }}><Link to="/shared" className="btn sm ghost">In Shared variables</Link></div>
                    ) : (
                      <div className="row" style={{ justifyContent: "flex-end" }}>
                        {!v.ref && <>
                          {v.key in shown
                            ? <button className="btn sm" aria-label={`Hide ${v.key}`} onClick={() => hide(v.key)}>Hide</button>
                            : <button className="btn sm" aria-label={`Show ${v.key}`} onClick={() => show(v.key)}>Show</button>}
                          <button className="btn sm" aria-label={`${copied === v.key ? "Copied" : "Copy"} ${v.key}`} onClick={() => copy(v.key)}><Icon name="copy" />{copied === v.key ? "Copied" : "Copy"}</button>
                        </>}
                        <Menu label={`Actions for ${v.key}`} trigger={<Icon name="more" />} className="btn sm ib">
                          <MenuItem onClick={() => edit(v)}>Edit value</MenuItem>
                          <MenuItem danger onClick={() => setDel(v)}>Delete</MenuItem>
                        </Menu>
                      </div>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {adding && <form className="card pad col w-form" onSubmit={add}>
        <h3>{editing ? `Edit ${key}` : "Add a variable"}</h3>
        <Field label="Key">{(id) => <input id={id} className="in mono" value={key} readOnly={editing} onChange={(e) => setKey(e.target.value.toUpperCase())} pattern="[A-Z_][A-Z0-9_]*" required />}</Field>
        {exists && !editing && <Callout tone="warn">{`${key} already exists. Adding replaces its value.`}</Callout>}
        {dbs.length > 0 && (
          <Field label="Reference" hint="A reference follows the database: change its password and every app gets the new URL.">
            {(id) => (
              <select id={id} className="in" value={ref} onChange={(e) => setRefTo(e.target.value)}>
                <option value="">None, type a value</option>
                {dbs.map((d) => <option key={d} value={`${d}.url`}>{`${d}.url`}</option>)}
              </select>
            )}
          </Field>
        )}
        {!ref && <Field label="Value">{(id) => <input id={id} className="in mono" type="password" autoComplete="off" value={value} onChange={(e) => setValue(e.target.value)} required />}</Field>}
        <div className="row">
          {!ref && !view.data?.form.image && <label className="row" style={{ gap: 6 }}><input type="checkbox" checked={build} onChange={(e) => setBuild(e.target.checked)} />Also at build time</label>}
          <label className="row" style={{ gap: 6 }}><input type="checkbox" checked={shared} disabled={editing} onChange={(e) => setShared(e.target.checked)} />Shared by every service in {env}</label>
        </div>
        <div className="row"><button className="btn pri" disabled={saving}>{exists ? "Replace variable" : "Add variable"}</button><button type="button" className="btn ghost" onClick={() => { reset(); setAdding(false); }}>Cancel</button></div>
      </form>}
      {other && <Diff project={project} env={env} other={other} service={service} />}
      {del && (
        <Confirm title={`Delete ${del.key}?`} yes="Yes, delete" onYes={() => api("DELETE", `${del.scope === "shared" ? envPath : svcPath}/env/${del.key}`).then(list.reload)} onClose={() => setDel(null)}>
          <p>The running version keeps it until the next deploy.</p>
        </Confirm>
      )}
      <Sources project={project} env={env} />
    </div>
  );
}
