import { useState } from "react";
import { api } from "../../api";
import { Callout, Confirm, Field, Icon, Loading, errText, useLoad } from "../../ui";

type Hook = { set: boolean; host: string };
type Mail = { set: boolean; host: string; port: string; username: string; from: string; to: string };
type View = { slack: Hook; discord: Hook; webhook?: Hook; ntfy?: Hook; email: Mail };

const base = "/api/settings/notifications";

export function Notifications({ only }: { only?: string }) {
  const v = useLoad<View>(base);
  if (!v.data) return <Loading what={v} />;
  const show = (c: string) => !only || only === c;
  return (
    <div className="col w-form">
      {show("slack") && <HookCard name="Slack" path="slack" hook={v.data.slack} reload={v.reload} hint="Slack, Apps, Incoming Webhooks, Add to a channel." />}
      {show("discord") && <HookCard name="Discord" path="discord" hook={v.data.discord} reload={v.reload} hint="The channel's settings, Integrations, Webhooks, New Webhook, Copy URL." />}
      {show("email") && <MailCard key={String(v.data.email.set)} mail={v.data.email} reload={v.reload} />}
      {show("webhook") && <HookCard name="Webhook" path="webhook" label="Webhook URL" hook={v.data.webhook ?? { set: false, host: "" }} reload={v.reload} hint={'Every alert is POSTed as JSON: {"text": "…", "source": "yawl"}.'} />}
      {show("ntfy") && <HookCard name="ntfy" path="ntfy" label="ntfy topic URL" hook={v.data.ntfy ?? { set: false, host: "" }} reload={v.reload} hint="https://ntfy.sh/your-topic, or your own ntfy server." />}
    </div>
  );
}

function useAct(reload: () => void) {
  const [err, setErr] = useState("");
  const [note, setNote] = useState("");
  const act = async (f: () => Promise<unknown>, done = "") => {
    setErr("");
    setNote("");
    try {
      await f();
      setNote(done);
      reload();
    } catch (e) {
      setErr(errText(e));
    }
  };
  const [ask, setAsk] = useState(false);
  const confirm = (name: string, path: string) => ask && (
    <Confirm title={`Remove ${name === "Email" ? "email" : name}?`} yes="Yes, remove" onClose={() => setAsk(false)} onYes={async () => { await api("DELETE", `${base}/${path}`); reload(); }}>
      <p>Alerts stop going to it.</p>
    </Confirm>
  );
  return { err, note, act, setAsk, confirm };
}

function HookCard({ name, path, hook, reload, hint, label }: { name: string; path: string; hook: Hook; reload: () => void; hint: string; label?: string }) {
  const [url, setUrl] = useState("");
  const { err, note, act, setAsk, confirm } = useAct(reload);
  return (
    <div className="card pad col">
      <h2 className="row" style={{ gap: 8 }}><Icon name={path} />{name}</h2>
      {hook.set ? (
        <>
          <p>{`Posts to ${hook.host}.`}</p>
          <div className="row">
            <button className="btn" aria-label={`Send a test to ${name}`} onClick={() => act(() => api("POST", `${base}/${path}/test`), `Sent. Look in ${name}.`)}>Send a test</button>
            <button className="btn ghost" aria-label={`Remove ${name}`} onClick={() => setAsk(true)}>Remove</button>
          </div>
        </>
      ) : (
        <>
          <Field label={label ?? `${name} webhook URL`} hint={hint}>
            {(id) => <input id={id} className="in mono" type="url" autoComplete="off" value={url} onChange={(e) => setUrl(e.target.value)} />}
          </Field>
          <div><button className="btn pri" aria-label={`Save ${name}`} disabled={!url} onClick={() => act(() => api("PUT", `${base}/${path}`, { url }), "Saved.")}>Save</button></div>
        </>
      )}
      {note && <Callout tone="ok">{note}</Callout>}
      {err && <Callout tone="bad">{err}</Callout>}
      {confirm(name, path)}
    </div>
  );
}

function MailCard({ mail, reload }: { mail: Mail; reload: () => void }) {
  const [f, setF] = useState({ host: mail.host, port: mail.port || "587", username: mail.username, password: "", from: mail.from, to: mail.to });
  const { err, note, act, setAsk, confirm } = useAct(reload);
  const field = (label: string, k: keyof typeof f, type = "text") => (
    <Field label={label}>{(id) => <input id={id} className="in mono" type={type} autoComplete="off" value={f[k]} onChange={(e) => setF({ ...f, [k]: e.target.value })} />}</Field>
  );
  return (
    <div className="card pad col">
      <h2 className="row" style={{ gap: 8 }}><Icon name="mail" />Email</h2>
      {mail.set && <p>{`Mails ${mail.to} through ${mail.host}.`}</p>}
      <div className="grid2">
        {field("SMTP host", "host")}
        {field("Port", "port")}
        {field("Username", "username")}
        {field("Password", "password", "password")}
        {field("From", "from")}
        {field("To", "to")}
      </div>
      <p className="muted">Port 465 is TLS from the start; any other port must offer STARTTLS. Leave the password empty to keep the one saved.</p>
      <div className="row">
        <button className="btn pri" aria-label="Save email" onClick={() => act(() => api("PUT", `${base}/email`, f), "Saved.")}>Save</button>
        {mail.set && <button className="btn" aria-label="Send a test to email" onClick={() => act(() => api("POST", `${base}/email/test`), `Sent. Look in ${mail.to}.`)}>Send a test</button>}
        {mail.set && <button className="btn ghost" aria-label="Remove email" onClick={() => setAsk(true)}>Remove</button>}
      </div>
      {note && <Callout tone="ok">{note}</Callout>}
      {err && <Callout tone="bad">{err}</Callout>}
      {confirm("Email", "email")}
    </div>
  );
}
