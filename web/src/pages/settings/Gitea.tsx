import { useState } from "react";
import { api } from "../../api";
import { Callout, Confirm, Copy, Field, Loading, TestConnection, errText, useLoad } from "../../ui";

type Host = { host: string; webhook_url: string; secret?: string };

const base = "/api/settings/gitea";

export function Gitea() {
  const list = useLoad<Host[]>(base);
  const [host, setHost] = useState("codeberg.org");
  const [token, setToken] = useState("");
  const [made, setMade] = useState<Host | null>(null);
  const [err, setErr] = useState("");
  const [ask, setAsk] = useState("");
  if (!list.data) return <Loading what={list} />;

  async function connect() {
    setErr("");
    try {
      setMade(await api<Host>("PUT", `${base}/${host.trim()}`, { token }));
      setToken("");
      list.reload();
    } catch (e) {
      setErr(errText(e));
    }
  }

  return (
    <div className="col w-form">
      {list.data.map((h) => (
        <div key={h.host} className="card pad col">
          <h2>{h.host}</h2>
          <div className="row"><span className="muted">Webhook</span><code>{h.webhook_url}</code><Copy text={h.webhook_url} /></div>
          <TestConnection label={`Test ${h.host}`} path={`${base}/${h.host}/test`} />
          <div><button className="btn ghost" aria-label={`Forget ${h.host}`} onClick={() => setAsk(h.host)}>Forget</button></div>
        </div>
      ))}
      {ask && <Confirm title={`Forget ${ask}?`} yes="Yes, forget" onClose={() => setAsk("")} onYes={async () => { await api("DELETE", `${base}/${ask}`); list.reload(); }}>
        <p>Services that build from it stop deploying on push.</p>
      </Confirm>}
      {made?.secret && (
        <Callout tone="ok" title={`${made.host} is connected.`}>
          <div className="row"><span className="muted">Webhook URL</span><code>{made.webhook_url}</code><Copy text={made.webhook_url} /></div>
          <div className="row"><span className="muted">Secret</span><code>{made.secret}</code><Copy text={made.secret} /></div>
          In Gitea or Forgejo, Settings → Webhooks → Add webhook: push and pull request events. The secret is not shown again.
        </Callout>
      )}
      <div className="card pad col">
        <h2>Connect Gitea</h2>
        <Field label="Host">{(id) => <input id={id} className="in mono" value={host} onChange={(e) => setHost(e.target.value)} />}</Field>
        <Field label="Access token" hint="With read access to repositories; with write access to repositories and issues too, Yawl sets commit statuses and comments the preview address on pull requests.">
          {(id) => <input id={id} className="in mono" type="password" autoComplete="off" value={token} onChange={(e) => setToken(e.target.value)} />}
        </Field>
        <div><button className="btn pri" disabled={!token || !host} onClick={connect}>Connect</button></div>
        {err && <Callout tone="bad">{err}</Callout>}
      </div>
    </div>
  );
}
