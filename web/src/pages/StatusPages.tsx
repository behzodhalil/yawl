import { useState } from "react";
import { Shell } from "../Shell";
import { api } from "../api";
import { navigate } from "../router";
import { Callout, Confirm, Empty, Field, Link, Loading, Modal, Row, StatusBadge, errText, useLoad, type BadgeKind } from "../ui";

type SPRow = { id?: string; name: string; subject: string };
type Host = { host: string; dns: string; dns_detail: string; cert: string };
type SPage = { id: string; slug: string; title: string; server_id: string; domain: string; watcher: boolean; rows: SPRow[]; url: string; hosts: Host[] };
type SPList = { pages: SPage[]; choices: { subject: string; label: string }[]; servers: { id: string; name: string }[] };
type Update = { id: string; at: number; body: string };
type Inc = { id: string; kind: "incident" | "maintenance"; title: string; rows: string[]; state: string; auto: boolean; starts_at: number; ends_at: number; resolved_at: number; created_at: number; updates: Update[] };

const LIST = "/api/status-pages";
const MON = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];
const two = (n: number) => String(n).padStart(2, "0");
const when = (ms: number) => { const d = new Date(ms); return `${d.getDate()} ${MON[d.getMonth()]} ${two(d.getHours())}:${two(d.getMinutes())}`; };

function hostState(h: Host): [BadgeKind, string] {
  if (h.dns === "ok" && h.cert === "issued") return ["ok", "Live"];
  if (h.dns === "ok") return ["busy", "Certificate pending"];
  if (h.dns === "wrong") return ["bad", "DNS not set"];
  return ["off", "Not checked yet"];
}

export function StatusPages() {
  const list = useLoad<SPList>(LIST);
  return (
    <Shell crumb={["Status pages"]} active="/status-pages">
      <div className="ph">
        <h1>Status pages</h1>
        <Link to="/status-pages/new" className="btn pri">New status page</Link>
      </div>
      {!list.data ? <Loading what={list} /> : list.data.pages.length === 0 ? (
        <Empty text="No status pages yet. A page shows the services you pick, under names you choose, at an address of yours." />
      ) : (
        <div className="card scroll">
          <table className="tbl">
            <thead><tr><th>Page</th><th>Addresses</th><th>Services shown</th></tr></thead>
            <tbody>{list.data.pages.map((p) => (
              <tr key={p.id}>
                <td><Link to={`/status-pages/${p.id}`}><b>{p.title}</b></Link>{p.watcher && <div className="dim">Also on the watcher</div>}</td>
                <td><div className="col" style={{ gap: 6 }}>{p.hosts.length === 0 ? <span className="dim">Routed within a minute</span> : p.hosts.map((h) => {
                  const [k, w] = hostState(h);
                  return <div key={h.host} className="row" style={{ gap: 8, flexWrap: "wrap" }}><a className="mono" href={"https://" + h.host} target="_blank" rel="noreferrer">{h.host}</a><StatusBadge kind={k}>{w}</StatusBadge></div>;
                })}</div></td>
                <td className="num">{p.rows.length}</td>
              </tr>
            ))}</tbody>
          </table>
        </div>
      )}
    </Shell>
  );
}

export function StatusPage({ id }: { id: string }) {
  const list = useLoad<SPList>(LIST);
  if (!list.data) return <Shell crumb={["Status pages"]} active="/status-pages"><Loading what={list} /></Shell>;
  const page = list.data.pages.find((p) => p.id === id);
  if (id !== "new" && !page) {
    return <Shell crumb={[<Link key="l" to="/status-pages">Status pages</Link>, "Not found"]} active="/status-pages"><Empty text="There is no such status page." /></Shell>;
  }
  return (
    <Shell crumb={[<Link key="l" to="/status-pages">Status pages</Link>, page?.title ?? "New"]} active="/status-pages">
      <div className="ph">
        <h1>{page?.title ?? "New status page"}</h1>
        {page && <div className="row" style={{ gap: 8 }}>
          {page.url && <a className="btn" href={page.url} target="_blank" rel="noreferrer">Open</a>}
          <a className="btn" href={`${LIST}/${page.id}/preview`} target="_blank" rel="noreferrer">Preview</a>
        </div>}
      </div>
      {page && <Incidents page={page} />}
      <PageForm key={page?.id ?? "new"} list={list.data} page={page} onSaved={(p) => { list.reload(); if (!page) navigate(`/status-pages/${p.id}`, true); }} />
    </Shell>
  );
}

function PageForm({ list, page, onSaved }: { list: SPList; page?: SPage; onSaved: (p: SPage) => void }) {
  const [title, setTitle] = useState(page?.title ?? "");
  const [slug, setSlug] = useState(page?.slug ?? "");
  const [server, setServer] = useState(page?.server_id ?? list.servers[0]?.id ?? "");
  const [domain, setDomain] = useState(page?.domain ?? "");
  const [watcher, setWatcher] = useState(page?.watcher ?? false);
  const [rows, setRows] = useState<SPRow[]>(page?.rows ?? []);
  const [err, setErr] = useState("");
  const [saved, setSaved] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const put = (i: number, r: Partial<SPRow>) => setRows(rows.map((x, k) => (k === i ? { ...x, ...r } : x)));
  const move = (i: number, d: number) => { const n = [...rows]; [n[i], n[i + d]] = [n[i + d], n[i]]; setRows(n); };
  const wrong = page?.hosts.filter((h) => h.dns === "wrong" && h.dns_detail) ?? [];
  return (
    <>
      <Row title="Page" hint="The title is the first thing a visitor reads. The short name makes the page's free address.">
        <Field label="Title">{(fid) => <input id={fid} className="in" value={title} maxLength={120} onChange={(e) => setTitle(e.target.value)} />}</Field>
        <Field label="Short name" hint={slug ? `Free address: ${slug}.status.<server address>.sslip.io` : "Lowercase letters, digits and dashes."}>
          {(fid) => <input id={fid} className="in mono" value={slug} onChange={(e) => setSlug(e.target.value)} />}
        </Field>
      </Row>
      <Row title="Where it is served" hint="Caddy on this server serves the page as a file; visitors never reach the panel. Pick a server other than your apps' when you have one.">
        <Field label="Server">{(fid) => (
          <select id={fid} className="in" value={server} onChange={(e) => setServer(e.target.value)}>
            {list.servers.map((s) => <option key={s.id} value={s.id}>{s.name}</option>)}
          </select>
        )}</Field>
        <Field label="Domain" hint="Optional, for example status.acme.uz: an A record to the server's address.">
          {(fid) => <input id={fid} className="in mono" value={domain} placeholder="status.acme.uz" onChange={(e) => setDomain(e.target.value)} />}
        </Field>
        {wrong.map((h) => <Callout key={h.host} tone="warn">{h.dns_detail}</Callout>)}
        <label className="row" style={{ gap: 8 }}>
          <input type="checkbox" checked={watcher} onChange={(e) => setWatcher(e.target.checked)} />
          Also on the watcher
        </label>
        <p className="muted">The watcher serves this page from Cloudflare too, so it stays up when your servers are down. One page at most.</p>
      </Row>
      <Row title="Rows" hint="Each row has the name visitors read. A project row covers every service in it. Nothing is shown until you add a row.">
        {rows.map((r, i) => (
          <div key={i} className="row" style={{ gap: 8, flexWrap: "wrap" }}>
            <input className="in" style={{ flex: "1 1 160px", width: "auto" }} aria-label={`Public name, row ${i + 1}`} value={r.name} maxLength={60} onChange={(e) => put(i, { name: e.target.value })} />
            <select className="in" style={{ flex: "1 1 200px", width: "auto" }} aria-label={`Covers, row ${i + 1}`} value={r.subject} onChange={(e) => put(i, { subject: e.target.value })}>
              {list.choices.map((c) => <option key={c.subject} value={c.subject}>{c.label}</option>)}
            </select>
            <button className="btn sm ghost" disabled={i === 0} onClick={() => move(i, -1)}>Up</button>
            <button className="btn sm ghost" disabled={i === rows.length - 1} onClick={() => move(i, 1)}>Down</button>
            <button className="btn sm ghost" onClick={() => setRows(rows.filter((_, k) => k !== i))}>Remove</button>
          </div>
        ))}
        <div><button className="btn" disabled={rows.length >= 50 || list.choices.length === 0} onClick={() => setRows([...rows, { name: "", subject: list.choices[0].subject }])}>Add row</button></div>
      </Row>
      <div className="row" style={{ gap: 8, padding: "16px 0" }}>
        <button className="btn pri" disabled={!title || !slug || !server} onClick={async () => {
          setErr(""); setSaved(false);
          try {
            const p = await api<SPage>(page ? "PUT" : "POST", page ? `${LIST}/${page.id}` : LIST, { title, slug, server_id: server, domain, watcher, rows });
            setSaved(true); onSaved(p);
          } catch (e) { setErr(errText(e)); }
        }}>{page ? "Save" : "Make the page"}</button>
        {page && <button className="btn danger" onClick={() => setDeleting(true)}>Delete page</button>}
        {saved && <span className="muted">Saved. The page changes within a minute.</span>}
      </div>
      {err && <Callout tone="bad">{err}</Callout>}
      {deleting && page && (
        <Modal title={`Delete ${page.title}?`} onClose={() => setDeleting(false)}>
          <p>Its addresses stop answering within a minute, and its incidents are deleted with it.</p>
          <div className="row">
            <button className="btn danger" onClick={async () => {
              try { await api("DELETE", `${LIST}/${page.id}`); navigate("/status-pages"); } catch (e) { setErr(errText(e)); setDeleting(false); }
            }}>Delete page</button>
            <button className="btn ghost" onClick={() => setDeleting(false)}>Cancel</button>
          </div>
        </Modal>
      )}
    </>
  );
}

function badgeOf(i: Inc, now: number): [BadgeKind, string] {
  if (i.state !== "published") return ["off", i.auto ? "Automatic draft" : "Draft"];
  if (i.kind === "incident") return i.resolved_at ? ["ok", "Resolved"] : ["busy", "Ongoing"];
  if (now < i.starts_at) return ["off", "Scheduled"];
  return now < i.ends_at ? ["busy", "Maintenance"] : ["ok", "Completed"];
}

function Incidents({ page }: { page: SPage }) {
  const path = `${LIST}/${page.id}/incidents`;
  const list = useLoad<Inc[]>(path);
  const [writing, setWriting] = useState<"" | "incident" | "maintenance">("");
  const now = Date.now();
  const shown = (list.data ?? []).filter((i) => i.state !== "dismissed");
  const drafts = shown.filter((i) => i.state === "draft");
  const published = shown.filter((i) => i.state === "published");
  return (
    <Row title="Public incidents" hint="What visitors read about an outage, in your words. When a row goes down, a draft is written for you; it shows only once you publish it.">
      {!list.data ? <Loading what={list} /> : (
        <>
          {drafts.length === 0 && published.length === 0 && <p className="muted">No public incidents in the last 90 days.</p>}
          {[...drafts, ...published].map((i) => <IncidentCard key={i.id} i={i} page={page} now={now} reload={list.reload} />)}
        </>
      )}
      {writing ? <NewIncident kind={writing} page={page} path={path} onDone={() => { setWriting(""); list.reload(); }} /> : (
        <div className="row" style={{ gap: 8 }}>
          <button className="btn" onClick={() => setWriting("incident")}>New public incident</button>
          <button className="btn" onClick={() => setWriting("maintenance")}>Schedule maintenance</button>
        </div>
      )}
    </Row>
  );
}

function IncidentCard({ i, page, now, reload }: { i: Inc; page: SPage; now: number; reload: () => void }) {
  const [text, setText] = useState("");
  const [err, setErr] = useState("");
  const [deleting, setDeleting] = useState(false);
  const [kind, word] = badgeOf(i, now);
  const names = i.rows.map((r) => page.rows.find((x) => x.id === r)?.name).filter(Boolean).join(", ");
  const act = async (method: string, url: string, body?: unknown) => {
    setErr("");
    try { await api(method, url, body); setText(""); reload(); } catch (e) { setErr(errText(e)); }
  };
  const open = i.state === "published" && (i.kind === "incident" ? !i.resolved_at : now < i.ends_at);
  return (
    <div className="card pad col" style={{ gap: 8 }}>
      <div className="row" style={{ gap: 8, flexWrap: "wrap" }}>
        <b>{i.title}</b><StatusBadge kind={kind}>{word}</StatusBadge>
        <span className="muted">{i.kind === "maintenance" ? `${when(i.starts_at)} to ${when(i.ends_at)}` : when(i.created_at)}{names && `. Affects ${names}`}</span>
      </div>
      {i.updates.map((u) => <div key={u.id} className="col" style={{ gap: 2 }}><span className="dim">{when(u.at)}</span><span style={{ whiteSpace: "pre-wrap" }}>{u.body}</span></div>)}
      {open && (
        <>
          <textarea className="in" style={{ height: 72, padding: 8 }} aria-label={`Update for ${i.title}`} maxLength={2000} value={text} onChange={(e) => setText(e.target.value)} />
          <div className="row" style={{ gap: 8 }}>
            <button className="btn sm" disabled={!text.trim()} onClick={() => act("POST", `/api/status-incidents/${i.id}/updates`, { body: text })}>Post update</button>
            {i.kind === "incident" && <button className="btn sm" disabled={!text.trim()} onClick={() => act("POST", `/api/status-incidents/${i.id}/updates`, { body: text, resolve: true })}>Post and resolve</button>}
          </div>
        </>
      )}
      <div className="row" style={{ gap: 8 }}>
        {i.state === "draft" && <button className="btn sm pri" onClick={() => act("POST", `/api/status-incidents/${i.id}/publish`)}>Publish</button>}
        {i.auto && i.state === "draft"
          ? <button className="btn sm ghost" onClick={() => act("DELETE", `/api/status-incidents/${i.id}`)}>Dismiss</button>
          : <button className="btn sm ghost" onClick={() => setDeleting(true)}>Delete</button>}
      </div>
      {err && <Callout tone="bad">{err}</Callout>}
      {deleting && (
        <Confirm title="Delete this public incident?" yes="Yes, delete" onClose={() => setDeleting(false)}
          onYes={async () => { await api("DELETE", `/api/status-incidents/${i.id}`); reload(); }}>
          <p>Visitors stop seeing it on the status page.</p>
        </Confirm>
      )}
    </div>
  );
}

function NewIncident({ kind, page, path, onDone }: { kind: "incident" | "maintenance"; page: SPage; path: string; onDone: () => void }) {
  const [title, setTitle] = useState("");
  const [rows, setRows] = useState<string[]>([]);
  const [body, setBody] = useState("");
  const [starts, setStarts] = useState("");
  const [ends, setEnds] = useState("");
  const [err, setErr] = useState("");
  const send = async (publish: boolean) => {
    setErr("");
    const ms = (v: string) => (v ? new Date(v).getTime() : 0);
    try { await api("POST", path, { kind, title, rows, body, publish, starts_at: ms(starts), ends_at: ms(ends) }); onDone(); } catch (e) { setErr(errText(e)); }
  };
  return (
    <div className="card pad col">
      <h3>{kind === "incident" ? "New public incident" : "Schedule maintenance"}</h3>
      <Field label="Title">{(fid) => <input id={fid} className="in" value={title} maxLength={120} onChange={(e) => setTitle(e.target.value)} />}</Field>
      {kind === "maintenance" && (
        <div className="row" style={{ gap: 8, flexWrap: "wrap" }}>
          <Field label="Starts">{(fid) => <input id={fid} className="in" type="datetime-local" value={starts} onChange={(e) => setStarts(e.target.value)} />}</Field>
          <Field label="Ends">{(fid) => <input id={fid} className="in" type="datetime-local" value={ends} onChange={(e) => setEnds(e.target.value)} />}</Field>
        </div>
      )}
      <div className="col" style={{ gap: 6 }}>
        <span>Affects</span>
        {page.rows.map((r) => (
          <label key={r.id} className="row" style={{ gap: 8 }}>
            <input type="checkbox" checked={rows.includes(r.id!)} onChange={(e) => setRows(e.target.checked ? [...rows, r.id!] : rows.filter((x) => x !== r.id))} />
            {r.name}
          </label>
        ))}
      </div>
      <Field label="First update">{(fid) => <textarea id={fid} className="in" style={{ height: 72, padding: 8 }} maxLength={2000} value={body} onChange={(e) => setBody(e.target.value)} />}</Field>
      <div className="row" style={{ gap: 8 }}>
        <button className="btn pri" disabled={!title} onClick={() => send(true)}>Publish</button>
        <button className="btn" disabled={!title} onClick={() => send(false)}>Save as draft</button>
        <button className="btn ghost" onClick={onDone}>Cancel</button>
      </div>
      {err && <Callout tone="bad">{err}</Callout>}
    </div>
  );
}
