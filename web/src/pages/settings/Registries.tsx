import { useState, type FormEvent } from "react";
import { api } from "../../api";
import { Callout, Confirm, Field, Loading, TestConnection, errText, useLoad } from "../../ui";

type Login = { host: string; username: string };

export function Registries() {
  const list = useLoad<Login[]>("/api/registries");
  const [host, setHost] = useState("");
  const [user, setUser] = useState("");
  const [token, setToken] = useState("");
  const [err, setErr] = useState("");
  const [ask, setAsk] = useState<Login | null>(null);

  async function add(e: FormEvent) {
    e.preventDefault();
    setErr("");
    try {
      await api("PUT", `/api/registries/${encodeURIComponent(host.trim())}`, { username: user, token });
      setHost(""); setUser(""); setToken("");
      list.reload();
    } catch (e) {
      setErr(errText(e));
    }
  }

  return (
    <div className="col w-form">
      {!list.data ? <Loading what={list} /> : list.data.length > 0 && (
        <table className="tbl">
          <thead><tr><th>Host</th><th>User</th><th /></tr></thead>
          <tbody>
            {list.data.map((r) => (
              <tr key={r.host}>
                <td className="mono">{r.host}</td><td>{r.username}</td>
                <td data-u="right"><div className="row" style={{ justifyContent: "flex-end", alignItems: "flex-start" }}><TestConnection label={`Test ${r.host}`} path={`/api/registries/${encodeURIComponent(r.host)}/test`} /><button className="btn sm danger" aria-label={`Remove ${r.host}`} onClick={() => setAsk(r)}>Remove</button></div></td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      <form className="col" onSubmit={add}>
        <div className="grid2">
          <Field label="Host">{(id) => <input id={id} className="in mono" placeholder="ghcr.io" value={host} onChange={(e) => setHost(e.target.value)} />}</Field>
          <Field label="User">{(id) => <input id={id} className="in" value={user} onChange={(e) => setUser(e.target.value)} />}</Field>
        </div>
        <Field label="Token">{(id) => <input id={id} className="in mono" type="password" autoComplete="off" value={token} onChange={(e) => setToken(e.target.value)} />}</Field>
        <div><button className="btn" disabled={!host.trim() || !user || !token}>Add</button></div>
      </form>
      {err && <Callout tone="bad">{err}</Callout>}
      {ask && <Confirm title={`Remove ${ask.host}?`} yes="Yes, remove" onClose={() => setAsk(null)} onYes={async () => { await api("DELETE", `/api/registries/${encodeURIComponent(ask.host)}`); list.reload(); }}>
        <p>Services that pull private images from it can no longer deploy.</p>
      </Confirm>}
    </div>
  );
}
