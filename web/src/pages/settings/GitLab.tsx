import { useState } from "react";
import { api } from "../../api";
import { Callout, Confirm, Copy, Field, Loading, TestConnection, errText, useLoad } from "../../ui";

type Host = { host: string; webhook_url: string; secret?: string };

const base = "/api/settings/gitlab";
const noHook = "None yet: GitLab cannot reach this panel's address. Start the panel with --public-url and its https address.";

export function GitLab() {
  const list = useLoad<Host[]>(base);
  const status = useLoad<{ gitlab: string }>("/api/settings/commit-status");
  const [host, setHost] = useState("gitlab.com");
  const [token, setToken] = useState("");
  const [ask, setAsk] = useState("");
  const [made, setMade] = useState<Host | null>(null);
  const [err, setErr] = useState("");
  const [note, setNote] = useState("");
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
          <div className="row"><span className="muted">Webhook</span>{h.webhook_url ? <><code>{h.webhook_url}</code><Copy text={h.webhook_url} /></> : <span>{noHook}</span>}</div>
          <TestConnection label={`Test ${h.host}`} path={`${base}/${h.host}/test`} />
          <div><button className="btn ghost" aria-label={`Forget ${h.host}`} onClick={() => setAsk(h.host)}>Forget</button></div>
        </div>
      ))}
      {note && <Callout tone="warn">{note}</Callout>}
      {ask && <Confirm title={`Forget ${ask}?`} yes="Yes, forget" onClose={() => setAsk("")} onYes={async () => { setNote((await api<{ note?: string } | null>("DELETE", `${base}/${ask}`))?.note ?? ""); list.reload(); }}>
        <p>Services that build from it stop deploying on push.</p>
      </Confirm>}
      {status.data?.gitlab && (
        <Callout tone="warn">{`Commit statuses were refused (${status.data.gitlab}): give the token the api scope.`}</Callout>
      )}
      {made?.secret && (
        <Callout tone="ok" title={`${made.host} is connected.`}>
          {!made.webhook_url && <p>{noHook}</p>}
          <div className="row"><span className="muted">Webhook URL</span>{made.webhook_url ? <><code>{made.webhook_url}</code><Copy text={made.webhook_url} /></> : <span>(none yet)</span>}</div>
          <div className="row"><span className="muted">Secret</span><code>{made.secret}</code><Copy text={made.secret} /></div>
          In GitLab, a group or project, Settings → Webhooks: push events and merge request events. The secret is not shown again.
        </Callout>
      )}
      <div className="card pad col">
        <h2>Connect GitLab</h2>
        <Field label="Host">{(id) => <input id={id} className="in mono" value={host} onChange={(e) => setHost(e.target.value)} />}</Field>
        <Field label="Access token" hint="read_repository; with api too, Yawl comments the preview address on merge requests.">
          {(id) => <input id={id} className="in mono" type="password" autoComplete="off" value={token} onChange={(e) => setToken(e.target.value)} />}
        </Field>
        <div><button className="btn pri" disabled={!token || !host} onClick={connect}>Connect</button></div>
        {err && <Callout tone="bad">{err}</Callout>}
      </div>
    </div>
  );
}
