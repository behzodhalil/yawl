import { useState } from "react";
import { api } from "../api";
import { sealTo } from "../seal";
import { Shell } from "../Shell";
import { ServerTabs } from "./ServerTabs";
import { Callout, Field, Link, Loading, errText, useBusy, useLoad } from "../ui";

export function Drain({ name }: { name: string }) {
  const path = `/api/servers/${encodeURIComponent(name)}/drain`;
  const st = useLoad<{ set: boolean; url: string; user: string; agent_pubkey: string }>(path);
  const [url, setUrl] = useState("");
  const [user, setUser] = useState("");
  const [token, setToken] = useState("");
  const [err, setErr] = useState("");
  const [busy, once] = useBusy();
  const save = () => once(async () => {
    setErr("");
    try {
      if (!st.data?.agent_pubkey) throw new Error(`Server ${name} has no secrets key; join it again.`);
      const token_box = token ? await sealTo(st.data.agent_pubkey, new TextEncoder().encode(token.trim())) : "";
      await api("PUT", path, { url: url.trim(), user: user.trim(), token_box });
      setToken("");
      st.reload();
    } catch (e) {
      setErr(errText(e));
    }
  });
  const stop = () => once(async () => {
    try { await api("DELETE", path); st.reload(); } catch (e) { setErr(errText(e)); }
  });
  return (
    <Shell crumb={[<Link to="/servers">Servers</Link>, name, "Log drain"]} active="/servers">
      <h1>{name}</h1>
      <ServerTabs name={name} active="drain" />
      {!st.data ? <Loading what={st} /> : (
        <div className="card pad col" style={{ maxWidth: 720 }}>
          {st.data.set ? (
            <>
              <p>{`Every app's logs on ${name} go to ${st.data.url}.`}</p>
              <p className="muted">Each service's Logs tab can read them back from there.</p>
              <div><button className="btn" disabled={busy} onClick={stop}>Stop sending</button></div>
            </>
          ) : (
            <>
              <Field label="Loki address" hint="Grafana Cloud's is on the Loki stack's page, like https://logs-prod-012.grafana.net.">
                {(id) => <input id={id} value={url} onChange={(e) => setUrl(e.target.value)} placeholder="https://" />}
              </Field>
              <Field label="User" hint="Grafana Cloud: the stack's user number. Empty for a token alone, or no login.">
                {(id) => <input id={id} value={user} onChange={(e) => setUser(e.target.value)} />}
              </Field>
              <Field label="Token">{(id) => <input id={id} type="password" value={token} onChange={(e) => setToken(e.target.value)} />}</Field>
              <div className="row"><button className="btn pri" disabled={!url || busy} onClick={save}>Send logs to Loki</button>{!url && <span className="muted">Loki address is needed</span>}</div>
            </>
          )}
          {err && <Callout tone="bad">{err}</Callout>}
        </div>
      )}
    </Shell>
  );
}
