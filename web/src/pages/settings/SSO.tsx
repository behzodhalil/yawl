import { useState, type FormEvent } from "react";
import { Button, SensitiveInput, Switch } from "@cloudflare/kumo";
import { api } from "../../api";
import { Callout, Confirm, Copy, Field, Loading, errText, useBusy, useLoad, Input, Card, notify } from "../../ui";

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
    setMsg(null);
    notify("Saved.");
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
    <Card title="Sign-in with SSO" className="max-w-[var(--w-form)]">
      <form className="flex flex-col gap-4" onSubmit={save}>
        <p className="text-kumo-subtle">An OpenID Connect provider that says whether an email is verified: Google Workspace, Okta, Authentik, Keycloak and others; Microsoft Entra does not, so it cannot sign members in. Give it the callback URL below. A member signs in with a verified email of theirs; nobody joins by SSO alone.</p>
        {callback ? (
          <Field label="Callback URL">
            {(id) => (
              <div className="flex items-center gap-2.5">
                <Input id={id} className="font-mono text-sm" readOnly value={callback} />
                <Copy text={callback} />
              </div>
            )}
          </Field>
        ) : (
          <p className="text-kumo-subtle">No callback address yet. Start the panel with --public-url and its https address.</p>
        )}
        <div className="grid gap-4 sm:grid-cols-2">
          <Field label="Name to show" hint="Like Acme Google">{(id) => <Input id={id} value={f.name} onChange={up("name")} />}</Field>
          <Field label="Issuer" hint="Like https://accounts.google.com; empty turns SSO off">{(id) => <Input id={id} className="font-mono text-sm" value={f.issuer} onChange={up("issuer")} />}</Field>
          <Field label="Client ID">{(id) => <Input id={id} className="font-mono text-sm" value={f.client_id} onChange={up("client_id")} />}</Field>
          <Field label="Client secret" hint={data.secret_set ? "The secret is set; leave it empty to keep it." : undefined}>
            {(id) => <SensitiveInput id={id} className="font-mono text-sm" autoComplete="off" value={f.client_secret} onChange={up("client_secret")} />}
          </Field>
        </div>
        <Switch label="Only SSO: password sign-in is refused, except for the oldest Admin" checked={f.only} disabled={locked} onClick={() => setF({ ...f, only: !f.only })} />
        {locked && <span className="text-sm text-kumo-subtle">Sign in with SSO once, in a private window, to turn this on.</span>}
        {msg && <Callout tone={msg[0] as "ok" | "bad"}>{msg[1]}</Callout>}
        <div className="flex flex-wrap items-center gap-3 border-t border-kumo-hairline pt-4"><Button type="submit" variant="primary" loading={busy}>Save</Button></div>
        {off && (
          <Confirm title="Turn SSO off?" yes="Yes, turn off" onYes={put} onClose={() => setOff(false)}>
            <p>Members sign in with their passwords again.</p>
          </Confirm>
        )}
      </form>
    </Card>
  );
}
