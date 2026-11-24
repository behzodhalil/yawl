import { type ReactNode, useEffect, useState } from "react";
import { ago, dayMonth } from "../../format";
import { api } from "../../api";
import { Callout, Confirm, Copy, Icon, Loading, StatusBadge, errText, useLoad } from "../../ui";

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
  <><hr className="tg-hr" /><button className="tg-switch" onClick={onClick}>{children}<Icon name="chev" /></button></>
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
  const ended = r.ended?.why === "already" ? <p className="muted">{r.ended.title || "This chat"} was already connected.</p>
    : r.ended?.why === "expired" ? (
      <>
        <Callout tone="warn">The link expired before a chat used it.</Callout>
        <div className="row"><button className="btn" onClick={() => connect(group)}>Try again</button></div>
      </>
    ) : null;
  const names = new Map((servers.data ?? []).map((s) => [s.id, s.name]));
  const gets = (rt?: Route) => {
    const s = (rt?.servers ?? []).map((id) => names.get(id)).filter(Boolean), p = rt?.projects ?? [];
    return s.length + p.length ? [s.join(", "), p.join(", ")].filter(Boolean).join(" · ") : "All alerts";
  };

  if (r.pending) return (
    <div className="card pad col tg w-form">
      <h2 className="row"><Icon name="telegram" />Telegram<StatusBadge kind="busy">Waiting</StatusBadge></h2>
      <div className="tg-wait">
        <div className="col">
          {group ? <p className="tg-lead">Pick the group in Telegram. @{bot} joins it and links it.</p>
            : <p className="tg-lead">Press <b>Start</b> in the chat with @{bot} that just opened.</p>}
          {group && (
            <div className="col" style={{ gap: 6 }}>
              <span className="muted tg-small">If @{bot} is in the group already, send this there:</span>
              <div className="row"><code>/start@{bot} {r.pending.code}</code><Copy text={`/start@${bot} ${r.pending.code}`} /></div>
            </div>
          )}
          <div className="row">
            <a className="btn" href={r.pending.link} target="_blank" rel="noopener"><Icon name="send" />Open Telegram again</a>
            <button className="btn" onClick={() => run(async () => { await api("DELETE", "/api/settings/telegram/connect"); setRelay({ ...r, pending: undefined }); })}>Cancel</button>
          </div>
          <p className="muted tg-small">The link works for 10 minutes.</p>
        </div>
        <figure className="tg-qr">
          <img alt="QR code of the Telegram link" src={"data:image/svg+xml;utf8," + encodeURIComponent(r.pending.qr)} />
          <figcaption className="muted tg-small">Scan with your phone</figcaption>
        </figure>
      </div>
      {bad}
    </div>
  );

  if (r.chats.length) return (
    <div className="card pad col tg w-form">
      <h2 className="row"><Icon name="telegram" />Telegram<StatusBadge kind="ok">Connected</StatusBadge></h2>
      <div className="tg-list">
        {r.chats.map((c) => {
          const person = c.type === "private";
          return (
            <div className="tg-chat" key={c.id}>
              <span className="tg-ava"><Icon name={person ? "person" : "users"} /></span>
              <div className="col tg-name">
                <span>{c.title || (person ? "Personal chat" : "Group")}</span>
                <span className="muted tg-small">{c.gone_at ? "Telegram stopped this chat: the bot was blocked or removed." : `${person ? "Personal chat" : "Group"} · ${dayMonth(c.linked_at)}`}</span>
                {!c.gone_at && <span className="tg-small">{gets(c.routes)}</span>}
              </div>
              <div className="row">
                {!c.gone_at && <button className="btn sm" aria-expanded={choosing === c.id} onClick={() => setChoosing(choosing === c.id ? "" : c.id)}>Choose</button>}
                {!c.gone_at && <button className="btn sm" onClick={() => run(async () => { await api("POST", `/api/settings/telegram/chats/${encodeURIComponent(c.id)}/test`); setNote("Sent. Check Telegram."); })}>Send test</button>}
                <button className="btn sm danger" onClick={() => setAsk(() => async () => { await api("DELETE", `/api/settings/telegram/chats/${encodeURIComponent(c.id)}`); setRelay({ ...r, chats: r.chats.filter((x) => x.id !== c.id) }); })}>Disconnect</button>
              </div>
              {choosing === c.id && (
                <Routes chat={c} servers={servers.data ?? []} projects={(projects.data ?? []).map((p) => p.name)} onCancel={() => setChoosing("")}
                  onSaved={(rt) => { setChoosing(""); setRelay({ ...r, chats: r.chats.map((x) => (x.id === c.id ? { ...x, routes: rt } : x)) }); }} />
              )}
            </div>
          );
        })}
      </div>
      <div className="row">
        {r.available && <button className="btn" disabled={r.chats.length >= 10} onClick={() => connect(true)}><Icon name="users" />Add a group</button>}
        {r.available && <button className="btn" disabled={r.chats.length >= 10} onClick={() => connect(false)}><Icon name="person" />Add another chat</button>}
        {bot && <p className="muted tg-small">Sent by @{bot}</p>}
      </div>
      {note && <p className="muted">{note}</p>}
      {ended}
      {bad}
      {ask && <Confirm title="Disconnect Telegram?" yes="Yes, disconnect" onClose={() => setAsk(null)} onYes={ask}>
        <p>Alerts stop going there.</p>
      </Confirm>}
      <Switch onClick={onOwn}>Use your own bot instead</Switch>
    </div>
  );

  return (
    <div className="card pad col tg w-form">
      <h2 className="row"><Icon name="telegram" />Telegram</h2>
      <p className="muted">Alerts from this panel, in a Telegram chat or group.</p>
      <div className="row">
        <button className="btn pri" onClick={() => connect(false)}><Icon name="send" />Connect Telegram</button>
        <button className="btn" onClick={() => connect(true)}><Icon name="users" />Connect a group</button>
      </div>
      {ended}
      {bad}
      <Switch onClick={onOwn}>Use your own bot</Switch>
    </div>
  );
}

function Routes({ chat, servers, projects, onCancel, onSaved }: {
  chat: Chat; servers: { id: string; name: string }[]; projects: string[]; onCancel: () => void; onSaved: (rt: Route) => void;
}) {
  const [rt, setRt] = useState<Route>({ servers: chat.routes?.servers ?? [], projects: chat.routes?.projects ?? [] });
  const [err, setErr] = useState("");
  const flip = (k: keyof Route, v: string, on: boolean) => setRt({ ...rt, [k]: on ? [...rt[k], v] : rt[k].filter((x) => x !== v) });
  const save = async () => {
    setErr("");
    try {
      await api("PUT", `/api/settings/telegram/chats/${encodeURIComponent(chat.id)}/routes`, rt);
      onSaved(rt);
    } catch (e) { setErr(errText(e)); }
  };
  const list = (k: keyof Route, legend: string, items: [string, string][], none: string) => (
    <fieldset>
      <legend>{legend}</legend>
      {items.length ? items.map(([v, label], i) => (
        <label key={v} className="row">
          <input type="checkbox" autoFocus={i === 0 && (k === "servers" || !servers.length)} checked={rt[k].includes(v)} onChange={(e) => flip(k, v, e.target.checked)} />{label}
        </label>
      )) : <span className="muted tg-small">{none}</span>}
    </fieldset>
  );
  return (
    <div className="tg-routes col" role="group" aria-label={`What ${chat.title || "this chat"} gets`} onKeyDown={(e) => e.key === "Escape" && onCancel()}>
      <p className="muted tg-small">Alerts about the servers or projects you tick come here; tick none for every alert. Alerts about no server or project, and messages from the watcher, go to every chat.</p>
      <div className="tg-routes-lists">
        {list("servers", "Servers", servers.map((s) => [s.id, s.name]), "No servers yet.")}
        {list("projects", "Projects", projects.map((p) => [p, p]), "No projects yet.")}
      </div>
      <div className="row">
        <button className="btn pri sm" onClick={save}>Save</button>
        <button className="btn sm" onClick={onCancel}>Cancel</button>
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
    <div className="card pad col tg w-form">
      <h2 className="row"><Icon name="telegram" />Telegram<StatusBadge kind="busy">Waiting</StatusBadge></h2>
      <p className="tg-lead">Send this code to @{tg.bot}.</p>
      <div className="row"><span className="tg-code mono">{tg.code}</span><Copy text={tg.code ?? ""} /></div>
      <div className="row">
        <a className="btn" href={`https://t.me/${tg.bot}`} target="_blank" rel="noopener"><Icon name="send" />Open @{tg.bot}</a>
        <button className="btn" onClick={cancel}>Cancel</button>
      </div>
      <p className="muted tg-small">This page notices within a few seconds. The code works for 10 minutes.</p>
      {bad}
    </div>
  );

  if (tg.state === "linked" && !changing) return (
    <div className="card pad col tg w-form">
      <h2 className="row"><Icon name="telegram" />Telegram<StatusBadge kind="ok">Connected</StatusBadge></h2>
      <div className="tg-chat">
        <span className="tg-ava"><Icon name="telegram" /></span>
        <span className="tg-name">{chatWords(tg)[0]}{chatWords(tg).length > 1 && <span className="muted tg-small"> · {chatWords(tg).slice(1).join(" · ")}</span>}</span>
        <div className="row">
          <button className="btn sm" onClick={() => run(async () => { await api("POST", "/api/settings/telegram/test"); setNote("Sent. Check Telegram."); })}>Send test</button>
          <button className="btn sm danger" onClick={() => setAsk(() => async () => { await api("DELETE", "/api/settings/telegram"); setTg({ state: "none" }); })}>Disconnect</button>
        </div>
      </div>
      <div className="row">
        <p className="muted tg-small">{tg.bot ? `Sent by @${tg.bot}, your own bot.` : "Sent by your own bot."}</p>
        <button className="btn sm" onClick={() => { setErr(""); setNote(""); setChanging(true); }}>Change token</button>
      </div>
      {note && <p className="muted">{note}</p>}
      {bad}
      {ask && <Confirm title="Disconnect Telegram?" yes="Yes, disconnect" onClose={() => setAsk(null)} onYes={ask}>
        <p>Alerts stop going there.</p>
      </Confirm>}
      {other}
    </div>
  );

  return (
    <div className="card pad col tg w-form">
      <h2 className="row"><Icon name="telegram" />Telegram</h2>
      <p className="muted">Alerts from this panel, in a Telegram chat or group.</p>
      <ol className="tg-steps">
        <li>
          <span className="tg-n" aria-hidden="true">1</span>
          <div className="col">
            <span className="tg-t">Create a bot</span>
            <p className="muted">In Telegram, open @BotFather and send <code>/newbot</code>.</p>
            <div><a className="btn sm" href="https://t.me/BotFather" target="_blank" rel="noopener"><Icon name="send" />Open @BotFather</a></div>
          </div>
        </li>
        <li>
          <span className="tg-n" aria-hidden="true">2</span>
          <div className="col">
            <label className="tg-t" htmlFor="tg-token">Paste its token</label>
            <input id="tg-token" className="in mono" type="password" autoComplete="off" aria-describedby="tg-token-hint" value={token} onChange={(e) => setToken(e.target.value)} />
            <span id="tg-token-hint" className="muted tg-small">Stored sealed with the server's own key.</span>
          </div>
        </li>
        <li>
          <span className="tg-n" aria-hidden="true">3</span>
          <div className="col">
            <span className="tg-t">Link it</span>
            <div className="row">
              <button className="btn pri" disabled={!token} onClick={link}>Link</button>
              {changing && <button className="btn" onClick={() => { setErr(""); setChanging(false); setToken(""); }}>Cancel</button>}
            </div>
          </div>
        </li>
      </ol>
      {bad}
      {!changing && other}
    </div>
  );
}
