import { useState, type FormEvent } from "react";
import { api } from "../../api";
import { Callout, Confirm, Copy, Field, Loading, errText, useBusy, useLoad } from "../../ui";

type S = { name: string; issuer: string; client_id: string; only: boolean; secret_set?: boolean; works?: boolean; callback?: string };

export function SSO() {
  const cur = useLoad<S>("/api/settings/sso");
  if (!cur.data) return <Loading what={cur} />;
  return <SSOForm data={cur.data} reload={cur.reload} />;
}

function SSOForm({ data, reload }: { data: S; reload: () => void }) {
  const [f, setF] = useState({ name: data.name, issuer: data.issuer, client_id: data.client_id, client_secret: "", only: data.only });
  const [msg, setMsg] = useState<[string, string] | null>(null);
  const [off, setOff] = useState(false);
  const [busy, run] = useBusy();
  const up = (k: "name" | "issuer" | "client_id" | "client_secret") => (e: React.ChangeEvent<HTMLInputElement>) => setF({ ...f, [k]: e.target.value });
  async function put() {
    await api("PUT", "/api/settings/sso", f);
    setF({ ...f, client_secret: "" });
    setMsg(["ok", "Saved."]);
    reload();
  }
  async function save(e: FormEvent) {
    e.preventDefault();
    if (data.issuer && !f.issuer.trim()) return setOff(true);
    await run(async () => {
      try {
        await put();
      } catch (x) {
        setMsg(["bad", errText(x)]);
      }
    });
  }
  const callback = data.callback && !data.callback.startsWith("/") ? data.callback : "";
  const locked = !data.works && !f.only;
  return (
    <form className="card pad col" style={{ maxWidth: 640 }} onSubmit={save}>
      <h2>Sign-in with SSO</h2>
      <p className="muted">An OpenID Connect provider that says whether an email is verified: Google Workspace, Okta, Authentik, Keycloak and others; Microsoft Entra does not, so it cannot sign members in. Give it the callback URL below. A member signs in with a verified email of theirs; nobody joins by SSO alone.</p>
      {callback ? (
        <Field label="Callback URL">
          {(id) => (
            <div className="row" style={{ flexWrap: "nowrap" }}>
              <input id={id} className="in mono" readOnly value={callback} />
              <Copy text={callback} />
            </div>
          )}
        </Field>
      ) : (
        <p className="muted">No callback address yet. Start the panel with --public-url and its https address.</p>
      )}
      <Field label="Name to show" hint="Like Acme Google">{(id) => <input id={id} className="in" value={f.name} onChange={up("name")} />}</Field>
      <Field label="Issuer" hint="Like https://accounts.google.com; empty turns SSO off">{(id) => <input id={id} className="in mono" value={f.issuer} onChange={up("issuer")} />}</Field>
      <Field label="Client ID">{(id) => <input id={id} className="in mono" value={f.client_id} onChange={up("client_id")} />}</Field>
      <Field label="Client secret" hint={data.secret_set ? "The secret is set; leave it empty to keep it." : undefined}>
        {(id) => <input id={id} className="in mono" type="password" autoComplete="off" value={f.client_secret} onChange={up("client_secret")} />}
      </Field>
      <label className="row"><input type="checkbox" checked={f.only} disabled={locked} onChange={(e) => setF({ ...f, only: e.target.checked })} />Only SSO: password sign-in is refused, except for the oldest Admin</label>
      {locked && <span className="hint">Sign in with SSO once, in a private window, to turn this on.</span>}
      {msg && <Callout tone={msg[0] as "ok" | "bad"}>{msg[1]}</Callout>}
      <div><button className="btn pri" disabled={busy}>Save</button></div>
      {off && (
        <Confirm title="Turn SSO off?" yes="Yes, turn off" onYes={put} onClose={() => setOff(false)}>
          <p>Members sign in with their passwords again.</p>
        </Confirm>
      )}
    </form>
  );
}
