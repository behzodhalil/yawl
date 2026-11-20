import { useState } from "react";
import { api } from "../../api";
import { ago } from "../../format";
import { setShared, type SharedScope } from "../../seal";
import { Callout, Field, Loading, Modal, errText, useLoad } from "../../ui";

type Entry = { scope: "team" | "server"; key: string; servers: string[]; missing?: string[]; stale?: string[]; build?: boolean; updated_at: number };

export function SharedVars() {
  const list = useLoad<Entry[]>("/api/shared-env");
  const servers = useLoad<{ servers: { name: string }[] }>("/api/shared-env/keys");
  const [key, setKey] = useState("");
  const [value, setValue] = useState("");
  const [where, setWhere] = useState<SharedScope>({ kind: "team" });
  const [build, setBuild] = useState(false);
  const [err, setErr] = useState("");
  const [removing, setRemoving] = useState<Entry | null>(null);
  async function set() {
    setErr("");
    try {
      await setShared(where, key.trim(), value, build);
      setKey(""), setValue(""), list.reload();
    } catch (e) {
      setErr(errText(e));
    }
  }
  async function remove(e: Entry) {
    const path = e.scope === "team" ? `/api/shared-env/team/${encodeURIComponent(e.key)}` : `/api/shared-env/servers/${encodeURIComponent(e.servers[0])}/${encodeURIComponent(e.key)}`;
    setRemoving(null);
    try { await api("DELETE", path); list.reload(); } catch (x) { setErr(errText(x)); }
  }
  return (
    <div className="col">
      <h2>Shared variables</h2>
      <p className="muted">Team variables reach every app, server variables every app on that server; an app's own variable wins. Changes apply at the next deploy.</p>
      {err && <Callout tone="bad">{err}</Callout>}
      {!list.data ? <Loading what={list} /> : (
        <div className="card scroll">
          <table className="tbl">
            <thead><tr><th>Name</th><th>Where</th><th>Changed</th><th /></tr></thead>
            <tbody>
              {list.data.length === 0 ? <tr><td colSpan={4} className="dim">None yet.</td></tr> : list.data.map((e) => (
                <tr key={e.scope + e.key + e.servers.join()}>
                  <td className="mono">{e.key}{e.build && <span className="dim"> (build too)</span>}</td>
                  <td>
                    {e.scope === "team" ? "Team" : `Server ${e.servers[0]}`}
                    {e.missing && e.missing.length > 0 && <div className="muted">{`Not on ${e.missing.join(", ")}: set it again to reach it.`}</div>}
                    {e.stale && e.stale.length > 0 && <div className="muted">{`${e.stale.join(", ")} joined again since: set it again, or its deploys stop.`}</div>}
                  </td>
                  <td className="dim">{ago(e.updated_at)}</td>
                  <td><button className="btn sm ghost" onClick={() => setRemoving(e)}>Delete</button></td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {removing && (
        <Modal title={`Delete ${removing.key}?`} onClose={() => setRemoving(null)}>
          <p>{removing.scope === "team" ? "Every app" : `Every app on ${removing.servers[0]}`} goes without it from its next deploy, unless it sets its own.</p>
          <div className="row">
            <button className="btn danger" onClick={() => remove(removing)}>{`Delete ${removing.key}`}</button>
            <button className="btn ghost" onClick={() => setRemoving(null)}>Cancel</button>
          </div>
        </Modal>
      )}
      <div className="card pad col">
        <h3>Set a variable</h3>
        <Field label="Name">{(id) => <input id={id} className="in mono" value={key} onChange={(e) => setKey(e.target.value)} placeholder="SENTRY_DSN" />}</Field>
        <Field label="Value">{(id) => <input id={id} className="in mono" type="password" autoComplete="off" value={value} onChange={(e) => setValue(e.target.value)} />}</Field>
        <Field label="Where">{(id) => (
          <select id={id} className="in" value={where.kind === "server" ? where.name : ""} onChange={(e) => setWhere(e.target.value ? { kind: "server", name: e.target.value } : { kind: "team" })}>
            <option value="">Team: every server</option>
            {servers.data?.servers.map((s) => <option key={s.name} value={s.name}>{`Server ${s.name}`}</option>)}
          </select>
        )}</Field>
        <label className="row"><input type="checkbox" checked={build} onChange={(e) => setBuild(e.target.checked)} />Also give it to builds</label>
        <div><button className="btn pri" disabled={!key || !value} onClick={set}>Set</button></div>
      </div>
    </div>
  );
}
