import { useState, type FormEvent } from "react";
import { api } from "../../api";
import { Ago, Callout, Confirm, Copy, Field, Loading, Modal, errText, useBusy, useLoad } from "../../ui";

type Tok = { id: string; name: string; last_used_at?: number; created_at: number; member?: string };

export function Tokens() {
  const list = useLoad<Tok[]>("/api/tokens");
  const [name, setName] = useState("");
  const [made, setMade] = useState("");
  const [err, setErr] = useState("");
  const [ask, setAsk] = useState<Tok | null>(null);
  const [busy, run] = useBusy();

  async function create(e: FormEvent) {
    e.preventDefault();
    setErr("");
    await run(async () => {
      try {
        setMade((await api<{ token: string }>("POST", "/api/tokens", { name })).token);
        setName("");
      } catch (e) {
        setErr(errText(e));
      }
    });
  }

  return (
    <div className="card pad col" style={{ maxWidth: 720 }}>
      <h2>API tokens</h2>
      <p className="muted">For the CLI and scripts: yawl login stores one. A token can do anything the dashboard can, except change the password.</p>
      {!list.data ? <Loading what={list} /> : (
        <table className="tbl">
          <thead><tr><th>Name</th><th>Last used</th><th>Created</th><th /></tr></thead>
          <tbody>
            {list.data.map((t) => (
              <tr key={t.id}>
                <td>{t.name}{t.member && <div className="dim">{t.member}</div>}</td><td><Ago ms={t.last_used_at} /></td><td><Ago ms={t.created_at} /></td>
                <td><button className="btn sm danger" aria-label={`Delete ${t.name}`} onClick={() => setAsk(t)}>Delete</button></td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      <form className="row" onSubmit={create}>
        <div style={{ flex: 1 }}><Field label="Name">{(id) => <input id={id} className="in" placeholder="laptop" value={name} onChange={(e) => setName(e.target.value)} required />}</Field></div>
        <button className="btn pri" disabled={busy}>Create token</button>
      </form>
      {err && <Callout tone="bad">{err}</Callout>}
      {ask && <Confirm title={`Delete the token "${ask.name}"?`} yes="Yes, delete" onClose={() => setAsk(null)} onYes={async () => { await api("DELETE", `/api/tokens/${ask.id}`); list.reload(); }}>
        <p>Anything using it, such as the CLI or a script, stops working.</p>
      </Confirm>}
      {made && (
        <Modal title="Your new token" onClose={() => (setMade(""), list.reload())} dismissible={false}>
          <p>Copy it now. It is not shown again.</p>
          <div className="card pad row" style={{ justifyContent: "space-between", flexWrap: "nowrap" }}><code style={{ wordBreak: "break-all" }}>{made}</code><Copy text={made} /></div>
          <div><button className="btn pri" onClick={() => (setMade(""), list.reload())}>Done</button></div>
        </Modal>
      )}
    </div>
  );
}
