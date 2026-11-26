import { useState } from "react";
import { Button, ClipboardText, SensitiveInput } from "@cloudflare/kumo";
import { api } from "../../api";
import { Callout, Confirm, Field, Loading, TestConnection, errText, useLoad, Input, Card } from "../../ui";

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
    <div className="flex max-w-[720px] flex-col gap-3">
      {list.data.map((h) => (
        <Card key={h.host} title={h.host} actions={<Button size="sm" variant="ghost" aria-label={`Forget ${h.host}`} onClick={() => setAsk(h.host)}>Forget</Button>}>
          <div className="flex flex-wrap items-center gap-2.5"><span className="text-kumo-subtle">Webhook</span>{h.webhook_url ? <><ClipboardText size="base" className="min-w-0 max-w-full" text={h.webhook_url} /></> : <span>{noHook}</span>}</div>
          <TestConnection label={`Test ${h.host}`} path={`${base}/${h.host}/test`} />
        </Card>
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
          <div className="flex flex-wrap items-center gap-2.5"><span className="text-kumo-subtle">Webhook URL</span>{made.webhook_url ? <><ClipboardText size="base" className="min-w-0 max-w-full" text={made.webhook_url} /></> : <span>(none yet)</span>}</div>
          <div className="flex flex-wrap items-center gap-2.5"><span className="text-kumo-subtle">Secret</span><ClipboardText size="base" className="min-w-0 max-w-full" text={made.secret} /></div>
          In GitLab, a group or project, Settings → Webhooks: push events and merge request events. The secret is not shown again.
        </Callout>
      )}
      <Card title="Connect GitLab">
        <div className="grid gap-4 sm:grid-cols-2">
          <Field label="Host">{(id) => <Input id={id} className="font-mono text-sm" value={host} onChange={(e) => setHost(e.target.value)} />}</Field>
          <Field label="Access token" hint="read_repository; with api too, Yawl comments the preview address on merge requests.">
            {(id) => <SensitiveInput id={id} className="font-mono text-sm" autoComplete="off" value={token} onChange={(e) => setToken(e.target.value)} />}
          </Field>
        </div>
        {err && <Callout tone="bad">{err}</Callout>}
        <div className="flex flex-wrap items-center gap-3 border-t border-kumo-hairline pt-4"><Button variant="primary" disabled={!token || !host} onClick={connect}>Connect</Button></div>
      </Card>
    </div>
  );
}
