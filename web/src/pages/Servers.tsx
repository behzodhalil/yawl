import { useContext } from "react";
import { ago, bytes } from "../format";
import { SessionContext } from "../session";
import { Shell } from "../Shell";
import { JoinCommand, MANAGED_NEEDS } from "./AddServer";
import type { ServerView } from "../types";
import { Badge, InlineCopyText, LayerCard, Meter, Table } from "@cloudflare/kumo";
import { Ago, ButtonLink, Callout, Empty, Icon, Link, Loading, POLL_MS, StatusBadge, useLoad } from "../ui";
import { Pager, SortTh, TableBar, useTable } from "../table";

export const WARN_PCT = 80;
export const DANGER_PCT = 90;

export type Level = "ok" | "warn" | "bad";

export const level = (pct: number): Level => (pct >= DANGER_PCT ? "bad" : pct >= WARN_PCT ? "warn" : "ok");

export const FILL: Record<Level, string> = {
  ok: "from-kumo-success via-kumo-success to-kumo-success",
  warn: "from-kumo-warning via-kumo-warning to-kumo-warning",
  bad: "from-kumo-danger via-kumo-danger to-kumo-danger",
};

export const TEXT: Record<Level, string | undefined> = { ok: undefined, warn: "text-kumo-warning", bad: "text-kumo-danger" };

function DiskMeter({ pct }: { pct: number }) {
  return <Meter label="Disk used" value={pct} customValue={`${pct}%`} className="w-32" indicatorClassName={FILL[level(pct)]} />;
}

export function ServerState({ s }: { s: ServerView }) {
  return !s.last_heartbeat ? <StatusBadge kind="off">Never connected</StatusBadge> : s.offline ? <StatusBadge kind="off">Offline</StatusBadge> : <StatusBadge kind="ok">Online</StatusBadge>;
}

export const specs = (s: ServerView) =>
  [s.cores && `${s.cores} ${s.cores === 1 ? "core" : "cores"}`, s.mem_total && bytes(s.mem_total)].filter(Boolean).join(", ");

const DISK_WARN_DAYS = 30;

const DISK_BAD_DAYS = 7;

const fullInDays = (s: ServerView) => (s.forecast?.growing ? s.forecast.days ?? null : null);

export function diskText(s: ServerView) {
  const days = fullInDays(s);
  return `Disk ${s.disk_used_pct}% used${days === null ? "" : `, full in about ${days} ${days === 1 ? "day" : "days"}`}`;
}

export type Attention = { key: "offline" | "firewall" | "disk" | "reboot"; text: string; bad: boolean };

export function attention(s: ServerView): Attention[] {
  const days = fullInDays(s);
  const soon = days !== null && days < DISK_WARN_DAYS;
  return [
    s.offline && { key: "offline", text: s.last_heartbeat ? `Offline, last seen ${ago(s.last_heartbeat)}` : "Offline", bad: true },
    s.firewall_off && { key: "firewall", text: "Firewall off", bad: false },
    s.reboot_required && { key: "reboot", text: "Reboot required", bad: false },
    (level(s.disk_used_pct) !== "ok" || soon) && { key: "disk", text: diskText(s), bad: level(s.disk_used_pct) === "bad" || (soon && days <= DISK_BAD_DAYS) },
  ].filter((c): c is Attention => !!c);
}

function Health({ items }: { items: Attention[] }) {
  if (items.length === 0) return <span className="text-kumo-subtle">Healthy</span>;
  return <div className="flex flex-wrap items-center gap-1.5">{items.map((c) => <Badge key={c.text} variant={c.bad ? "error" : "warning"}>{c.text}</Badge>)}</div>;
}

export function ServerRow({ s }: { s: ServerView }) {
  return (
    <Table.Row>
      <Table.Cell>
        <Link to={`/servers/${encodeURIComponent(s.name)}`}><b>{s.name}</b></Link><div><InlineCopyText variant="mono-secondary">{s.public_ip}</InlineCopyText></div>
        {specs(s) && <div className="text-kumo-subtle">{specs(s)}</div>}
      </Table.Cell>
      <Table.Cell><ServerState s={s} /></Table.Cell>
      <Table.Cell><Ago ms={s.last_heartbeat} /></Table.Cell>
      <Table.Cell><DiskMeter pct={s.disk_used_pct} /></Table.Cell>
      <Table.Cell><Health items={attention(s)} /></Table.Cell>
    </Table.Row>
  );
}

export function Servers() {
  const list = useLoad<ServerView[]>("/api/servers", POLL_MS);
  const managed = useContext(SessionContext).managed;
  const tbl = useTable(list.data ?? [], { text: (s) => [s.name, s.public_ip, s.private_ip ?? ""].join(" "),
    sort: { name: (s) => s.name, seen: (s) => s.last_heartbeat ?? 0, disk: (s) => s.disk_used_pct } });
  return (
    <Shell crumb={["Servers"]} active="/servers">
      <div className="flex flex-wrap items-end justify-between gap-4">
        <div><h1>Servers</h1><p className="mt-1 text-kumo-subtle">Each server runs one agent. The agent dials out to this panel, so no management port is open on it.</p></div>
        <ButtonLink to="/servers/new" variant="primary" icon={<Icon name="plus" />}>Add server</ButtonLink>
      </div>
      {!list.data ? <Loading what={list} /> : (
        <div className="flex flex-col gap-3">
          {list.data.length === 1 && !managed && (
            <Callout tone="warn">This machine watches itself. If it goes down, no message can be sent. A second machine fixes this in a later version.</Callout>
          )}
          {list.data.length === 0 ? (managed ? (
            <div className="flex flex-col gap-3"><h2>Add your first server</h2><p className="text-kumo-subtle">{MANAGED_NEEDS}</p><JoinCommand intro="Run the command on it; the server appears here within a minute." /></div>
          ) : <Empty text="No server has joined yet."><ButtonLink to="/servers/new" variant="primary">Add server</ButtonLink></Empty>) : (
            <>
              <TableBar t={tbl} noun="servers" find="Search servers" />
              <LayerCard className="overflow-x-auto p-0">
                <Table>
                  <Table.Header><Table.Row><SortTh t={tbl} k="name">Server</SortTh><Table.Head>Agent</Table.Head><SortTh t={tbl} k="seen">Last seen</SortTh><SortTh t={tbl} k="disk">Disk</SortTh><Table.Head>Health</Table.Head></Table.Row></Table.Header>
                  <Table.Body>
                    {tbl.rows.map((s) => <ServerRow key={s.id} s={s} />)}
                  </Table.Body>
                </Table>
              </LayerCard>
              <Pager page={tbl.page} pages={tbl.pages} onPage={tbl.setPage} />
            </>
          )}
        </div>
      )}
    </Shell>
  );
}
