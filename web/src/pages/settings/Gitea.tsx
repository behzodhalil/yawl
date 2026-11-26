import { useState } from "react";
import { Button, ClipboardText, SensitiveInput } from "@cloudflare/kumo";
import { api } from "../../api";
import { Callout, Confirm, Field, Loading, TestConnection, errText, useLoad, Input, Card } from "../../ui";

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
    <div className="flex max-w-[720px] flex-col gap-3">
      {list.data.map((h) => (
        <Card key={h.host} title={h.host} actions={<Button size="sm" variant="ghost" aria-label={`Forget ${h.host}`} onClick={() => setAsk(h.host)}>Forget</Button>}>
          <div className="flex flex-wrap items-center gap-2.5"><span className="text-kumo-subtle">Webhook</span><ClipboardText size="base" className="min-w-0 max-w-full" text={h.webhook_url} /></div>
          <TestConnection label={`Test ${h.host}`} path={`${base}/${h.host}/test`} />
        </Card>
      ))}
      {ask && <Confirm title={`Forget ${ask}?`} yes="Yes, forget" onClose={() => setAsk("")} onYes={async () => { await api("DELETE", `${base}/${ask}`); list.reload(); }}>
        <p>Services that build from it stop deploying on push.</p>
      </Confirm>}
      {made?.secret && (
        <Callout tone="ok" title={`${made.host} is connected.`}>
          <div className="flex flex-wrap items-center gap-2.5"><span className="text-kumo-subtle">Webhook URL</span><ClipboardText size="base" className="min-w-0 max-w-full" text={made.webhook_url} /></div>
          <div className="flex flex-wrap items-center gap-2.5"><span className="text-kumo-subtle">Secret</span><ClipboardText size="base" className="min-w-0 max-w-full" text={made.secret} /></div>
          In Gitea or Forgejo, Settings → Webhooks → Add webhook: push and pull request events. The secret is not shown again.
        </Callout>
      )}
      <Card title="Connect Gitea">
        <div className="grid gap-4 sm:grid-cols-2">
          <Field label="Host">{(id) => <Input id={id} className="font-mono text-sm" value={host} onChange={(e) => setHost(e.target.value)} />}</Field>
          <Field label="Access token" hint="With read access to repositories; with write access to repositories and issues too, Yawl sets commit statuses and comments the preview address on pull requests.">
            {(id) => <SensitiveInput id={id} className="font-mono text-sm" autoComplete="off" value={token} onChange={(e) => setToken(e.target.value)} />}
          </Field>
        </div>
        {err && <Callout tone="bad">{err}</Callout>}
        <div className="flex flex-wrap items-center gap-3 border-t border-kumo-hairline pt-4"><Button variant="primary" disabled={!token || !host} onClick={connect}>Connect</Button></div>
      </Card>
    </div>
  );
}
