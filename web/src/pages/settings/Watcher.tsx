import { useState } from "react";
import { Button, ClipboardText, cn, linkVariants, Loader, SensitiveInput } from "@cloudflare/kumo";
import { api } from "../../api";
import { secondsAgo } from "../../format";
import type { Heartbeat, WatchState } from "../../types";
import { Callout, Confirm, Field, Link, Loading, errText, useLoad, Input, Card } from "../../ui";

export function Watcher() {
  const tg = useLoad<{ state: string; relay?: { chats: { gone_at: number | null }[] } }>("/api/settings/telegram");
  const watch = useLoad<WatchState>("/api/settings/watch");
  const [token, setToken] = useState("");
  const [account, setAccount] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const [note, setNote] = useState("");
  const [stop, setStop] = useState(false);
  if (!tg.data || !watch.data) return <Loading what={!tg.data ? tg : watch} />;
  const w = watch.data;
  return (
    <div className="flex max-w-[720px] flex-col gap-3">
      <Card title="Watcher">
        <p className="text-kumo-subtle">A Cloudflare Worker in your own account checks your apps, servers and this panel every minute from outside, and alerts on Telegram even when this server is down.</p>
        {tg.data.state !== "linked" && !tg.data.relay?.chats.some((c) => !c.gone_at) ? (
          <Callout tone="warn" title="Link Telegram first: the watcher sends its alerts there."><Link to="/notifications/telegram" className={linkVariants({ variant: "inline" })}>Telegram settings</Link></Callout>
        ) : w.state === "on" ? (
          <>
            <Callout tone="ok" title={`Watching from Cloudflare: ${w.checks} checks, last run ${w.last_run ? secondsAgo(w.last_run) : "not yet"}`}>
              Status page: <a href={w.url + "/status"} target="_blank" rel="noreferrer" className={cn(linkVariants({ variant: "inline" }), "font-mono text-sm")}>{w.url + "/status"}</a>
            </Callout>
            {w.servers.filter((s) => s.state !== "watched").map((s) => (
              <Callout key={s.name} tone="warn">{`${s.name} is not watched yet: its agent has not taken the watcher's key.`}</Callout>
            ))}
            <div className="flex flex-wrap items-center gap-2.5">
              <Button variant="secondary-destructive" onClick={() => setStop(true)}>Stop using the watcher</Button>
            </div>
          </>
        ) : (
          <>
            {w.state === "error" && w.error && <Callout tone="bad">{w.error}</Callout>}
            <p className="text-kumo-subtle">Make an API token at dash.cloudflare.com → My Profile → API Tokens with two permissions, nothing else: <b>Account · Workers Scripts: Edit</b> and <b>Account · D1: Edit</b>.</p>
            <div className="grid gap-4 sm:grid-cols-2">
              <Field label="Cloudflare API token" hint="Stored sealed with the server's own key.">{(id) => <SensitiveInput id={id} className="font-mono text-sm" autoComplete="off" value={token} onChange={(e) => setToken(e.target.value)} />}</Field>
              <Field label="Account ID" hint="On the right of your Cloudflare dashboard's home page.">{(id) => <Input id={id} className="font-mono text-sm" value={account} placeholder={w.account} onChange={(e) => setAccount(e.target.value)} />}</Field>
            </div>
            <div className="flex flex-wrap items-center gap-3 border-t border-kumo-hairline pt-4"><Button icon={busy ? <span aria-hidden="true" className="inline-flex"><Loader size={14} /></span> : undefined} variant="primary" disabled={busy || !token || !(account || w.account)} onClick={async () => {
              setErr(""); setBusy(true);
              try { await api("PUT", "/api/settings/watch", { token, account: account || w.account }); setToken(""); watch.reload(); } catch (e) { setErr(errText(e)); }
              setBusy(false);
            }}>{busy ? "Deploying…" : "Deploy the watcher"}</Button></div>
          </>
        )}
        {note && <p className="text-kumo-subtle">{note}</p>}
        {err && <Callout tone="bad">{err}</Callout>}
      </Card>
      {stop && <Confirm title="Stop using the watcher?" yes="Yes, stop" onClose={() => setStop(false)} onYes={async () => { const r = await api<WatchState>("DELETE", "/api/settings/watch"); setNote(r.note ?? ""); watch.reload(); }}>
        <p>Apps, servers and this panel are no longer checked from outside, and heartbeats stop.</p>
      </Confirm>}
      {w.state === "on" && <Heartbeats />}
    </div>
  );
}

function Heartbeats() {
  const list = useLoad<Heartbeat[]>("/api/heartbeats");
  const [name, setName] = useState("");
  const [every, setEvery] = useState("1440");
  const [grace, setGrace] = useState("60");
  const [err, setErr] = useState("");
  const [ask, setAsk] = useState<Heartbeat | null>(null);
  return (
    <Card title="Heartbeats">
      <p className="text-kumo-subtle">For a job that runs outside Yawl: a laptop backup, a GitHub Action. Give it the URL to call when it finishes, for example <code>curl -fsS -m 10 &lt;url&gt;</code>. No call within its period plus the grace time opens an incident.</p>
      {!list.data ? <Loading what={list} /> : list.data.map((h) => (
        <div key={h.id} className="flex flex-wrap items-center gap-2">
          <b>{h.name}</b>
          <span className="text-kumo-subtle">every {h.period_s / 60} min, grace {h.grace_s / 60} min</span>
          <ClipboardText size="base" className="min-w-0 max-w-full" text={h.url} />
          <Button size="sm" variant="ghost" aria-label={`Delete ${h.name}`} onClick={() => setAsk(h)}>Delete</Button>
        </div>
      ))}
      <div className="grid gap-4 sm:grid-cols-[2fr_1fr_1fr]">
        <Field label="Heartbeat name">{(id) => <Input id={id} value={name} onChange={(e) => setName(e.target.value)} />}</Field>
        <Field label="Every (minutes)">{(id) => <Input id={id} type="number" min={1} value={every} onChange={(e) => setEvery(e.target.value)} />}</Field>
        <Field label="Grace (minutes)">{(id) => <Input id={id} type="number" min={0} value={grace} onChange={(e) => setGrace(e.target.value)} />}</Field>
      </div>
      <div className="flex flex-wrap items-center gap-3 border-t border-kumo-hairline pt-4"><Button disabled={!name} onClick={async () => {
        setErr("");
        try {
          await api("POST", "/api/heartbeats", { name, period_s: Number(every) * 60, grace_s: Number(grace) * 60 });
          setName(""); list.reload();
        } catch (e) { setErr(errText(e)); }
      }}>Add heartbeat</Button></div>
      {err && <Callout tone="bad">{err}</Callout>}
      {ask && <Confirm title={`Delete the heartbeat "${ask.name}"?`} yes="Yes, delete" onClose={() => setAsk(null)} onYes={async () => { await api("DELETE", `/api/heartbeats/${ask.id}`); list.reload(); }}>
        <p>The job can no longer report in, and its URL stops working.</p>
      </Confirm>}
    </Card>
  );
}
