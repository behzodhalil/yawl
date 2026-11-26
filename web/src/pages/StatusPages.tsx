import { useState } from "react";
import { Button, Checkbox, Combobox, LayerCard, Select, Switch, Table, Textarea, cn, linkVariants } from "@cloudflare/kumo";
import { Shell } from "../Shell";
import { api } from "../api";
import { navigate } from "../router";
import { ButtonLink, Callout, Confirm, Empty, Field, Link, Loading, Modal, Row, StatusBadge, errText, notify, useLoad, type BadgeKind, Input, Card } from "../ui";

type SPRow = { id?: string; name: string; subject: string };
type Host = { host: string; dns: string; dns_detail: string; cert: string };
type SPage = { id: string; slug: string; title: string; server_id: string; domain: string; watcher: boolean; rows: SPRow[]; url: string; hosts: Host[] };
type SPServer = { id: string; name: string };
type SPList = { pages: SPage[]; choices: { subject: string; label: string }[]; servers: SPServer[] };
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
      <div className="flex flex-wrap items-end justify-between gap-4">
        <h1>Status pages</h1>
        <ButtonLink to="/status-pages/new" variant="primary">New status page</ButtonLink>
      </div>
      {!list.data ? <Loading what={list} /> : list.data.pages.length === 0 ? (
        <Empty text="No status pages yet. A page shows the services you pick, under names you choose, at an address of yours." />
      ) : (
        <LayerCard className="overflow-x-auto p-0">
          <Table>
            <Table.Header><Table.Row><Table.Head>Page</Table.Head><Table.Head>Addresses</Table.Head><Table.Head>Services</Table.Head></Table.Row></Table.Header>
            <Table.Body>{list.data.pages.map((p) => (
              <Table.Row key={p.id}>
                <Table.Cell><Link to={`/status-pages/${p.id}`}><b className="font-semibold">{p.title}</b></Link>{p.watcher && <div className="text-kumo-subtle">Also on the watcher</div>}</Table.Cell>
                <Table.Cell><div className="flex flex-col gap-1.5">{p.hosts.length === 0 ? <span className="text-kumo-subtle">Routed within a minute</span> : p.hosts.map((h) => {
                  const [k, w] = hostState(h);
                  return <div key={h.host} className="flex flex-wrap items-center gap-2"><a className={cn(linkVariants({ variant: "inline" }), "font-mono text-sm")} href={"https://" + h.host} target="_blank" rel="noreferrer">{h.host}</a><StatusBadge kind={k}>{w}</StatusBadge></div>;
                })}</div></Table.Cell>
                <Table.Cell className="font-mono text-sm tabular-nums">{p.rows.length}</Table.Cell>
              </Table.Row>
            ))}</Table.Body>
          </Table>
        </LayerCard>
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
      <div className="flex flex-wrap items-end justify-between gap-4">
        <h1>{page?.title ?? "New status page"}</h1>
        {page && <div className="flex flex-wrap items-center gap-2">
          {page.url && <ButtonLink to={page.url} target="_blank" rel="noreferrer">Open</ButtonLink>}
          <ButtonLink to={`${LIST}/${page.id}/preview`} target="_blank" rel="noreferrer">Preview</ButtonLink>
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
  const [deleting, setDeleting] = useState(false);
  const put = (i: number, r: Partial<SPRow>) => setRows(rows.map((x, k) => (k === i ? { ...x, ...r } : x)));
  const move = (i: number, d: number) => { const n = [...rows]; [n[i], n[i + d]] = [n[i + d], n[i]]; setRows(n); };
  const wrong = page?.hosts.filter((h) => h.dns === "wrong" && h.dns_detail) ?? [];
  return (
    <>
      <Row title="Page" hint="The title is the first thing a visitor reads. The short name makes the page's free address.">
        <Field label="Title">{(fid) => <Input id={fid} value={title} maxLength={120} onChange={(e) => setTitle(e.target.value)} />}</Field>
        <Field label="Short name" hint={slug ? `Free address: ${slug}.status.<server address>.sslip.io` : "Lowercase letters, digits and dashes."}>
          {(fid) => <Input id={fid} className="font-mono" value={slug} onChange={(e) => setSlug(e.target.value)} />}
        </Field>
      </Row>
      <Row title="Where it is served" hint="Caddy on this server serves the page as a file; visitors never reach the panel. Pick a server other than your apps' when you have one.">
        <Combobox label="Server" items={list.servers} value={list.servers.find((s) => s.id === server) ?? null}
          itemToStringLabel={(s: SPServer) => s.name} isItemEqualToValue={(a: SPServer, b: SPServer) => a.id === b.id}
          onValueChange={(s) => { if (s) setServer((s as SPServer).id); }}>
          <Combobox.TriggerInput placeholder="Pick a server" />
          <Combobox.Content>
            <Combobox.Empty>No such server.</Combobox.Empty>
            <Combobox.List>{(s: SPServer) => <Combobox.Item key={s.id} value={s}>{s.name}</Combobox.Item>}</Combobox.List>
          </Combobox.Content>
        </Combobox>
        <Field label="Domain" hint="Optional, for example status.acme.uz: an A record to the server's address.">
          {(fid) => <Input id={fid} className="font-mono" value={domain} placeholder="status.acme.uz" onChange={(e) => setDomain(e.target.value)} />}
        </Field>
        {wrong.map((h) => <Callout key={h.host} tone="warn">{h.dns_detail}</Callout>)}
        <Switch label="Also on the watcher" checked={watcher} onCheckedChange={setWatcher} />
        <p className="text-sm text-kumo-subtle">The watcher serves this page from Cloudflare too, so it stays up when your servers are down. One page at most.</p>
      </Row>
      <Row title="Rows" hint="Each row has the name visitors read. A project row covers every service in it. Nothing is shown until you add a row.">
        {rows.map((r, i) => (
          <div key={i} className="flex flex-wrap items-center gap-2">
            <Input className="w-auto flex-[1_1_160px]" aria-label={`Public name, row ${i + 1}`} value={r.name} maxLength={60} onChange={(e) => put(i, { name: e.target.value })} />
            <Select className="flex-[1_1_200px]" aria-label={`Covers, row ${i + 1}`} value={r.subject} onValueChange={(v) => put(i, { subject: String(v) })}
              items={Object.fromEntries(list.choices.map((c) => [c.subject, c.label]))} />
            <Button size="sm" variant="ghost" disabled={i === 0} onClick={() => move(i, -1)}>Up</Button>
            <Button size="sm" variant="ghost" disabled={i === rows.length - 1} onClick={() => move(i, 1)}>Down</Button>
            <Button size="sm" variant="ghost" onClick={() => setRows(rows.filter((_, k) => k !== i))}>Remove</Button>
          </div>
        ))}
        <div><Button disabled={rows.length >= 50 || list.choices.length === 0} onClick={() => setRows([...rows, { name: "", subject: list.choices[0].subject }])}>Add row</Button></div>
      </Row>
      <div className="flex flex-wrap items-center gap-2 py-4">
        <Button variant="primary" disabled={!title || !slug || !server} onClick={async () => {
          setErr("");
          try {
            const p = await api<SPage>(page ? "PUT" : "POST", page ? `${LIST}/${page.id}` : LIST, { title, slug, server_id: server, domain, watcher, rows });
            notify("Status page saved", "success", "The page changes within a minute."); onSaved(p);
          } catch (e) { setErr(errText(e)); }
        }}>{page ? "Save" : "Make the page"}</Button>
        {page && <Button variant="secondary-destructive" onClick={() => setDeleting(true)}>Delete page</Button>}
      </div>
      {err && <Callout tone="bad">{err}</Callout>}
      {deleting && page && (
        <Modal title={`Delete ${page.title}?`} onClose={() => setDeleting(false)}>
          <p>Its addresses stop answering within a minute, and its incidents are deleted with it.</p>
          <div className="flex flex-wrap items-center gap-2.5">
            <Button variant="destructive" onClick={async () => {
              try { await api("DELETE", `${LIST}/${page.id}`); navigate("/status-pages"); } catch (e) { setErr(errText(e)); setDeleting(false); }
            }}>Delete page</Button>
            <Button variant="ghost" onClick={() => setDeleting(false)}>Cancel</Button>
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
          {drafts.length === 0 && published.length === 0 && <p className="text-kumo-subtle">No public incidents in the last 90 days.</p>}
          {[...drafts, ...published].map((i) => <IncidentCard key={i.id} i={i} page={page} now={now} reload={list.reload} />)}
        </>
      )}
      {writing ? <NewIncident kind={writing} page={page} path={path} onDone={() => { setWriting(""); list.reload(); }} /> : (
        <div className="flex flex-wrap items-center gap-2">
          <Button onClick={() => setWriting("incident")}>New public incident</Button>
          <Button onClick={() => setWriting("maintenance")}>Schedule maintenance</Button>
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
    <LayerCard className="flex flex-col gap-2 p-4">
      <div className="flex flex-wrap items-center gap-2">
        <b className="font-semibold">{i.title}</b><StatusBadge kind={kind}>{word}</StatusBadge>
        <span className="text-kumo-subtle">{[i.kind === "maintenance" ? `${when(i.starts_at)} to ${when(i.ends_at)}` : i.updates[0] && when(i.updates[0].at) === when(i.created_at) ? "" : when(i.created_at), names && `Affects ${names}`].filter(Boolean).join(". ")}</span>
      </div>
      {i.updates.map((u) => <div key={u.id} className="flex flex-col gap-0.5"><span className="text-kumo-subtle">{when(u.at)}</span><span className="whitespace-pre-wrap">{u.body}</span></div>)}
      {open && (
        <>
          <Textarea className="h-18" aria-label={`Update for ${i.title}`} maxLength={2000} value={text} onChange={(e) => setText(e.target.value)} />
          <div className="flex flex-wrap items-center gap-2">
            <Button size="sm" disabled={!text.trim()} onClick={() => act("POST", `/api/status-incidents/${i.id}/updates`, { body: text })}>Post update</Button>
            {i.kind === "incident" && <Button size="sm" disabled={!text.trim()} onClick={() => act("POST", `/api/status-incidents/${i.id}/updates`, { body: text, resolve: true })}>Post and resolve</Button>}
          </div>
        </>
      )}
      <div className="flex flex-wrap items-center gap-2">
        {i.state === "draft" && <Button size="sm" variant="primary" onClick={() => act("POST", `/api/status-incidents/${i.id}/publish`)}>Publish</Button>}
        {i.auto && i.state === "draft"
          ? <Button size="sm" variant="ghost" onClick={() => act("DELETE", `/api/status-incidents/${i.id}`)}>Dismiss</Button>
          : <Button size="sm" variant="ghost" onClick={() => setDeleting(true)}>Delete</Button>}
      </div>
      {err && <Callout tone="bad">{err}</Callout>}
      {deleting && (
        <Confirm title="Delete this public incident?" yes="Yes, delete" onClose={() => setDeleting(false)}
          onYes={async () => { await api("DELETE", `/api/status-incidents/${i.id}`); reload(); }}>
          <p>Visitors stop seeing it on the status page.</p>
        </Confirm>
      )}
    </LayerCard>
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
    <Card title={kind === "incident" ? "New public incident" : "Schedule maintenance"} level={3} className="max-w-[var(--w-form)]">
      <Field label="Title">{(fid) => <Input id={fid} value={title} maxLength={120} onChange={(e) => setTitle(e.target.value)} />}</Field>
      {kind === "maintenance" && (
        <div className="grid gap-4 sm:grid-cols-2">
          <Field label="Starts">{(fid) => <Input id={fid} type="datetime-local" value={starts} onChange={(e) => setStarts(e.target.value)} />}</Field>
          <Field label="Ends">{(fid) => <Input id={fid} type="datetime-local" value={ends} onChange={(e) => setEnds(e.target.value)} />}</Field>
        </div>
      )}
      <Checkbox.Group legend="Affects" value={rows} allValues={page.rows.map((r) => r.id!)} onValueChange={setRows}>
        {page.rows.map((r) => <Checkbox.Item key={r.id} value={r.id!} label={r.name} />)}
      </Checkbox.Group>
      <Field label="First update">{(fid) => <Textarea id={fid} className="h-18" maxLength={2000} value={body} onChange={(e) => setBody(e.target.value)} />}</Field>
      <div className="flex flex-wrap items-center gap-3 border-t border-kumo-hairline pt-4">
        <Button variant="primary" disabled={!title} onClick={() => send(true)}>Publish</Button>
        <Button disabled={!title} onClick={() => send(false)}>Save as draft</Button>
        <Button variant="ghost" onClick={onDone}>Cancel</Button>
      </div>
      {err && <Callout tone="bad">{err}</Callout>}
    </Card>
  );
}
