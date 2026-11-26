import { type ReactNode, useEffect, useRef, useState } from "react";
import { ago, dayMonth } from "../../format";
import { api } from "../../api";
import { Button, Checkbox, SensitiveInput } from "@cloudflare/kumo";
import { ButtonLink, Callout, Confirm, Copy, Icon, Loading, StatusBadge, errText, useLoad, Card } from "../../ui";

type Route = { servers: string[]; projects: string[] };
type Chat = { id: string; type: string; title: string; linked_at: number; gone_at: number | null; routes?: Route };
type Pending = { code: string; link: string; expires_at: number; qr: string; bot?: string };
type Ended = { why: "already" | "expired"; title?: string };
type Relay = { available: boolean; bot?: string; chats: Chat[]; pending?: Pending; ended?: Ended; error?: string };
type TG = { state: "none" | "pending" | "linked"; bot?: string; code?: string; chat_type?: string; chat_name?: string; linked_at?: string; relay?: Relay };

const chatWords = (t: TG) => [t.chat_name || (t.chat_type && t.chat_type !== "private" ? "Group" : "Personal chat"), t.chat_type && t.chat_type !== "private" ? "group" : "", t.linked_at ? "linked " + ago(Number(t.linked_at)) : ""].filter(Boolean);

export function Telegram() {
  const first = useLoad<TG>("/api/settings/telegram");
  const [tg, setTg] = useState<TG | null>(null);
  const [own, setOwn] = useState<boolean | null>(null);
  useEffect(() => { if (first.data) setTg(first.data); }, [first.data]);
  if (!tg) return <Loading what={first} />;
  const r = tg.relay;
  if (!r || !(r.available || r.chats.length)) return <OwnBot tg={tg} setTg={setTg} />;
  if (own ?? tg.state !== "none") {
    return <OwnBot tg={tg} setTg={setTg} other={r.available ? <Switch onClick={() => setOwn(false)}>Use @{r.bot} instead</Switch> : null} />;
  }
  return <Shared tg={tg} r={r} setTg={setTg} onOwn={() => setOwn(true)} />;
}

const Switch = ({ onClick, children }: { onClick: () => void; children: ReactNode }) => (
  <><hr className="m-0 w-full border-0 border-t border-kumo-hairline" /><button type="button" className="flex w-full items-center justify-between text-left font-medium [&_.ico]:text-kumo-subtle" onClick={onClick}>{children}<Icon name="chev" /></button></>
);

function Shared({ tg, r, setTg, onOwn }: { tg: TG; r: Relay; setTg: (t: TG) => void; onOwn: () => void }) {
  const [err, setErr] = useState("");
  const [note, setNote] = useState("");
  const [choosing, setChoosing] = useState("");
  const [ask, setAsk] = useState<(() => Promise<unknown>) | null>(null);
  // a group's link is ?startgroup: Telegram asks which group and adds the bot; ?start always opens the private chat
  const [groupMode, setGroupMode] = useState(false);
  const group = r.pending ? r.pending.link.includes("?startgroup=") : groupMode;
  useEffect(() => { if (r.pending) setGroupMode(group); }, [r.pending?.link]);
  const servers = useLoad<{ id: string; name: string }[]>(r.chats.length ? "/api/servers" : null);
  const projects = useLoad<{ name: string }[]>(r.chats.length ? "/api/projects" : null);
  const setRelay = (next: Relay) => setTg({ ...tg, relay: next });
  useEffect(() => {
    if (!r.pending) return;
    const t = setInterval(async () => {
      try { setTg(await api<TG>("GET", "/api/settings/telegram")); } catch (e) { setErr(errText(e)); }
    }, 2000);
    return () => clearInterval(t);
  }, [r.pending?.code]);
  const run = async (f: () => Promise<void>) => {
    setErr(""); setNote("");
    try { await f(); } catch (e) { setErr(errText(e)); }
  };
  // the tab opens on the click, before the answer: a tab opened after a wait is blocked as a pop-up
  const connect = (g: boolean) => {
    setGroupMode(g);
    const w = window.open("", "_blank");
    run(async () => {
      try {
        const p = await api<Pending>("POST", "/api/settings/telegram/connect", g ? { group: true } : undefined);
        if (w) { w.opener = null; w.location.href = p.link; }
        setRelay({ ...r, bot: p.bot || r.bot, pending: p });
      } catch (e) { w?.close(); throw e; }
    });
  };
  const bad = (err || r.error) && <Callout tone="bad">{err || r.error}</Callout>;
  const bot = r.pending?.bot || r.bot;
  const ended = r.ended?.why === "already" ? <p className="text-kumo-subtle">{r.ended.title || "This chat"} was already connected.</p>
    : r.ended?.why === "expired" ? (
      <>
        <Callout tone="warn">The link expired before a chat used it.</Callout>
        <div className="flex flex-wrap items-center gap-2.5"><Button onClick={() => connect(group)}>Try again</Button></div>
      </>
    ) : null;
  const names = new Map((servers.data ?? []).map((s) => [s.id, s.name]));
  const gets = (rt?: Route) => {
    const s = (rt?.servers ?? []).map((id) => names.get(id)).filter(Boolean), p = rt?.projects ?? [];
    return s.length + p.length ? [s.join(", "), p.join(", ")].filter(Boolean).join(" · ") : "All alerts";
  };

  if (r.pending) return (
    <Card title={<><Icon name="telegram" />Telegram<StatusBadge kind="busy">Waiting</StatusBadge></>} className="max-w-[var(--w-form)]">
      <div className="flex flex-wrap items-center gap-7">
        <div className="flex flex-[1_1_320px] flex-col gap-3.5">
          {group ? <p className="text-[15px]">Pick the group in Telegram. @{bot} joins it and links it.</p>
            : <p className="text-[15px]">Press <b>Start</b> in the chat with @{bot} that just opened.</p>}
          {group && (
            <div className="flex flex-col gap-1.5">
              <span className="text-sm text-kumo-subtle">If @{bot} is in the group already, send this there:</span>
              <div className="flex flex-wrap items-center gap-2.5"><code>/start@{bot} {r.pending.code}</code><Copy text={`/start@${bot} ${r.pending.code}`} /></div>
            </div>
          )}
          <div className="flex flex-wrap items-center gap-2.5">
            <ButtonLink to={r.pending.link} target="_blank" rel="noopener" icon={<Icon name="send" />}>Open Telegram again</ButtonLink>
            <Button onClick={() => run(async () => { await api("DELETE", "/api/settings/telegram/connect"); setRelay({ ...r, pending: undefined }); })}>Cancel</Button>
          </div>
          <p className="text-sm text-kumo-subtle">The link works for 10 minutes.</p>
        </div>
        <figure className="m-0 flex flex-col items-center gap-2">
          <img className="block size-[132px] bg-white" alt="QR code of the Telegram link" src={"data:image/svg+xml;utf8," + encodeURIComponent(r.pending.qr)} />
          <figcaption className="text-sm text-kumo-subtle">Scan with your phone</figcaption>
        </figure>
      </div>
      {bad}
    </Card>
  );

  if (r.chats.length) return (
    <Card title={<><Icon name="telegram" />Telegram<StatusBadge kind="ok">Connected</StatusBadge></>} className="max-w-[var(--w-form)]">
      <div className="flex flex-col divide-y divide-kumo-hairline">
        {r.chats.map((c) => {
          const person = c.type === "private";
          return (
            <div className="flex flex-wrap items-center gap-3 py-3 first:pt-0 last:pb-0" key={c.id}>
              <span className="grid size-8 flex-none place-items-center rounded-md bg-kumo-tint text-kumo-subtle"><Icon name={person ? "person" : "users"} /></span>
              <div className="flex min-w-0 flex-[1_1_160px] flex-col font-medium">
                <span>{c.title || (person ? "Personal chat" : "Group")}</span>
                <span className="text-sm font-normal text-kumo-subtle">{c.gone_at ? "Telegram stopped this chat: the bot was blocked or removed." : `${person ? "Personal chat" : "Group"} · ${dayMonth(c.linked_at)}`}</span>
                {!c.gone_at && <span className="text-sm font-normal">{gets(c.routes)}</span>}
              </div>
              <div className="flex flex-wrap items-center gap-2.5">
                {!c.gone_at && <Button size="sm" aria-expanded={choosing === c.id} onClick={() => setChoosing(choosing === c.id ? "" : c.id)}>Choose</Button>}
                {!c.gone_at && <Button size="sm" onClick={() => run(async () => { await api("POST", `/api/settings/telegram/chats/${encodeURIComponent(c.id)}/test`); setNote("Sent. Check Telegram."); })}>Send test</Button>}
                <Button size="sm" variant="ghost" className="text-kumo-danger" onClick={() => setAsk(() => async () => { await api("DELETE", `/api/settings/telegram/chats/${encodeURIComponent(c.id)}`); setRelay({ ...r, chats: r.chats.filter((x) => x.id !== c.id) }); })}>Disconnect</Button>
              </div>
              {choosing === c.id && (
                <Routes chat={c} servers={servers.data ?? []} projects={(projects.data ?? []).map((p) => p.name)} onCancel={() => setChoosing("")}
                  onSaved={(rt) => { setChoosing(""); setRelay({ ...r, chats: r.chats.map((x) => (x.id === c.id ? { ...x, routes: rt } : x)) }); }} />
              )}
            </div>
          );
        })}
      </div>
      <div className="flex flex-wrap items-center gap-2.5">
        {r.available && <Button size="sm" icon={<Icon name="users" />} disabled={r.chats.length >= 10} onClick={() => connect(true)}>Add a group</Button>}
        {r.available && <Button size="sm" icon={<Icon name="person" />} disabled={r.chats.length >= 10} onClick={() => connect(false)}>Add another chat</Button>}
        {bot && <p className="text-sm text-kumo-subtle">Sent by @{bot}</p>}
      </div>
      {note && <p className="text-kumo-subtle">{note}</p>}
      {ended}
      {bad}
      {ask && <Confirm title="Disconnect Telegram?" yes="Yes, disconnect" onClose={() => setAsk(null)} onYes={ask}>
        <p>Alerts stop going there.</p>
      </Confirm>}
      <Switch onClick={onOwn}>Use your own bot instead</Switch>
    </Card>
  );

  return (
    <Card title={<><Icon name="telegram" />Telegram</>} className="max-w-[var(--w-form)]">
      <p className="text-kumo-subtle">Alerts from this panel, in a Telegram chat or group.</p>
      <div className="flex flex-wrap items-center gap-2.5">
        <Button variant="primary" icon={<Icon name="send" />} onClick={() => connect(false)}>Connect Telegram</Button>
        <Button icon={<Icon name="users" />} onClick={() => connect(true)}>Connect a group</Button>
      </div>
      {ended}
      {bad}
      <Switch onClick={onOwn}>Use your own bot</Switch>
    </Card>
  );
}

function Routes({ chat, servers, projects, onCancel, onSaved }: {
  chat: Chat; servers: { id: string; name: string }[]; projects: string[]; onCancel: () => void; onSaved: (rt: Route) => void;
}) {
  const [rt, setRt] = useState<Route>({ servers: chat.routes?.servers ?? [], projects: chat.routes?.projects ?? [] });
  const [err, setErr] = useState("");
  const box = useRef<HTMLDivElement>(null);
  useEffect(() => box.current?.querySelector<HTMLElement>("[role=checkbox]")?.focus(), []);
  const flip = (k: keyof Route, v: string, on: boolean) => setRt({ ...rt, [k]: on ? [...rt[k], v] : rt[k].filter((x) => x !== v) });
  const save = async () => {
    setErr("");
    try {
      await api("PUT", `/api/settings/telegram/chats/${encodeURIComponent(chat.id)}/routes`, rt);
      onSaved(rt);
    } catch (e) { setErr(errText(e)); }
  };
  const list = (k: keyof Route, legend: string, items: [string, string][], none: string) => (
    <fieldset className="m-0 flex min-w-40 flex-col gap-1.5 border-0 p-0">
      <legend className="mb-1.5 p-0 font-medium">{legend}</legend>
      {items.length ? items.map(([v, label]) => (
        <Checkbox key={v} label={label} checked={rt[k].includes(v)} onCheckedChange={(c) => flip(k, v, !!c)} />
      )) : <span className="text-sm text-kumo-subtle">{none}</span>}
    </fieldset>
  );
  return (
    <div ref={box} className="flex flex-[1_1_100%] flex-col gap-3 rounded-lg border border-kumo-line bg-kumo-tint p-3.5" role="group" aria-label={`What ${chat.title || "this chat"} gets`} onKeyDown={(e) => e.key === "Escape" && onCancel()}>
      <p className="text-sm text-kumo-subtle">Alerts about the servers or projects you tick come here; tick none for every alert. Alerts about no server or project, and messages from the watcher, go to every chat.</p>
      <div className="flex flex-wrap gap-x-10 gap-y-3">
        {list("servers", "Servers", servers.map((s) => [s.id, s.name]), "No servers yet.")}
        {list("projects", "Projects", projects.map((p) => [p, p]), "No projects yet.")}
      </div>
      <div className="flex flex-wrap items-center gap-2.5">
        <Button variant="primary" size="sm" onClick={save}>Save</Button>
        <Button size="sm" onClick={onCancel}>Cancel</Button>
      </div>
      {err && <Callout tone="bad">{err}</Callout>}
    </div>
  );
}

function OwnBot({ tg, setTg: setAll, other }: { tg: TG; setTg: (t: TG) => void; other?: ReactNode }) {
  const setTg = (t: TG) => setAll({ ...t, relay: t.relay ?? tg.relay });
  const [kept, setKept] = useState<TG | null>(null);
  const [changing, setChanging] = useState(false);
  const [ask, setAsk] = useState<(() => Promise<unknown>) | null>(null);
  const [token, setToken] = useState("");
  const [err, setErr] = useState("");
  const [note, setNote] = useState("");
  useEffect(() => {
    if (tg.state !== "pending") { setKept(null); return; }
    const t = setInterval(async () => {
      try { setAll(await api<TG>("GET", "/api/settings/telegram")); } catch (e) { setErr(errText(e)); }
    }, 2000);
    return () => clearInterval(t);
  }, [tg.state]);
  const run = async (f: () => Promise<void>) => {
    setErr(""); setNote("");
    try { await f(); } catch (e) { setErr(errText(e)); }
  };
  const link = () => run(async () => {
    const next = await api<TG>("POST", "/api/settings/telegram", { token });
    if (changing && tg.state === "linked") setKept(tg);
    setTg(next); setToken(""); setChanging(false);
  });
  const cancel = () => run(async () => {
    if (kept) { setTg(kept); setKept(null); return; }
    await api("DELETE", "/api/settings/telegram");
    setTg({ state: "none" });
  });
  const bad = err && <Callout tone="bad">{err}</Callout>;

  if (tg.state === "pending") return (
    <Card title={<><Icon name="telegram" />Telegram<StatusBadge kind="busy">Waiting</StatusBadge></>} className="max-w-[var(--w-form)]">
      <p className="text-[15px]">Send this code to @{tg.bot}.</p>
      <div className="flex flex-wrap items-center gap-2.5"><span className="rounded-lg border border-kumo-line bg-kumo-tint px-4 py-1.5 font-mono text-3xl font-medium tracking-[.08em]">{tg.code}</span><Copy text={tg.code ?? ""} /></div>
      <div className="flex flex-wrap items-center gap-2.5">
        <ButtonLink to={`https://t.me/${tg.bot}`} target="_blank" rel="noopener" icon={<Icon name="send" />}>Open @{tg.bot}</ButtonLink>
        <Button onClick={cancel}>Cancel</Button>
      </div>
      <p className="text-sm text-kumo-subtle">This page notices within a few seconds. The code works for 10 minutes.</p>
      {bad}
    </Card>
  );

  if (tg.state === "linked" && !changing) return (
    <Card title={<><Icon name="telegram" />Telegram<StatusBadge kind="ok">Connected</StatusBadge></>} className="max-w-[var(--w-form)]">
      <div className="flex flex-wrap items-center gap-3">
        <span className="grid size-8 flex-none place-items-center rounded-md bg-kumo-tint text-kumo-subtle"><Icon name="telegram" /></span>
        <span className="min-w-0 flex-[1_1_160px] font-medium">{chatWords(tg)[0]}{chatWords(tg).length > 1 && <span className="text-sm font-normal text-kumo-subtle"> · {chatWords(tg).slice(1).join(" · ")}</span>}</span>
        <div className="flex flex-wrap items-center gap-2.5">
          <Button size="sm" onClick={() => run(async () => { await api("POST", "/api/settings/telegram/test"); setNote("Sent. Check Telegram."); })}>Send test</Button>
          <Button size="sm" variant="ghost" className="text-kumo-danger" onClick={() => setAsk(() => async () => { await api("DELETE", "/api/settings/telegram"); setTg({ state: "none" }); })}>Disconnect</Button>
        </div>
      </div>
      <div className="flex flex-wrap items-center gap-2.5">
        <p className="text-sm text-kumo-subtle">{tg.bot ? `Sent by @${tg.bot}, your own bot.` : "Sent by your own bot."}</p>
        <Button size="sm" onClick={() => { setErr(""); setNote(""); setChanging(true); }}>Change token</Button>
      </div>
      {note && <p className="text-kumo-subtle">{note}</p>}
      {bad}
      {ask && <Confirm title="Disconnect Telegram?" yes="Yes, disconnect" onClose={() => setAsk(null)} onYes={ask}>
        <p>Alerts stop going there.</p>
      </Confirm>}
      {other}
    </Card>
  );

  return (
    <Card title={<><Icon name="telegram" />Telegram</>} className="max-w-[var(--w-form)]">
      <p className="text-kumo-subtle">Alerts from this panel, in a Telegram chat or group.</p>
      <ol className="m-0 mt-1 flex list-none flex-col gap-5 p-0">
        <li className="flex gap-3.5">
          <span className="grid size-6 flex-none place-items-center rounded-full border border-kumo-line text-xs font-semibold" aria-hidden="true">1</span>
          <div className="flex min-w-0 flex-1 flex-col gap-2.5">
            <span className="font-medium">Create a bot</span>
            <p className="text-kumo-subtle">In Telegram, open @BotFather and send <code>/newbot</code>.</p>
            <div><ButtonLink to="https://t.me/BotFather" target="_blank" rel="noopener" size="sm" icon={<Icon name="send" />}>Open @BotFather</ButtonLink></div>
          </div>
        </li>
        <li className="flex gap-3.5">
          <span className="grid size-6 flex-none place-items-center rounded-full border border-kumo-line text-xs font-semibold" aria-hidden="true">2</span>
          <div className="flex min-w-0 flex-1 flex-col gap-2.5">
            <label className="font-medium" htmlFor="tg-token" id="tg-token-label">Paste its token</label>
            <SensitiveInput id="tg-token" className="max-w-[460px] font-mono text-sm" autoComplete="off" aria-describedby="tg-token-hint" value={token} onChange={(e) => setToken(e.target.value)} />
            <span id="tg-token-hint" className="text-sm text-kumo-subtle">Stored sealed with the server's own key.</span>
          </div>
        </li>
        <li className="flex gap-3.5">
          <span className="grid size-6 flex-none place-items-center rounded-full border border-kumo-line text-xs font-semibold" aria-hidden="true">3</span>
          <div className="flex min-w-0 flex-1 flex-col gap-2.5">
            <span className="font-medium">Link it</span>
            <div className="flex flex-wrap items-center gap-2.5">
              <Button variant="primary" disabled={!token} onClick={link}>Link</Button>
              {changing && <Button onClick={() => { setErr(""); setChanging(false); setToken(""); }}>Cancel</Button>}
            </div>
          </div>
        </li>
      </ol>
      {bad}
      {!changing && other}
    </Card>
  );
}
