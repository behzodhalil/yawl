import { useContext } from "react";
import { ago, bytes, forecastLine } from "../format";
import { SessionContext } from "../session";
import { Shell } from "../Shell";
import { JoinCommand, MANAGED_NEEDS } from "./AddServer";
import type { ServerView } from "../types";
import { Callout, Empty, Icon, Link, Loading, POLL_MS, StatusBadge, useLoad } from "../ui";
import { Pager, SortTh, TableBar, useTable } from "../table";

function Bar({ pct }: { pct: number }) {
  return <div className={"bar " + (pct >= 95 ? "bad" : pct >= 85 ? "warn" : "")} style={{ width: 120 }}><i style={{ width: pct + "%" }} /></div>;
}

export function ServerState({ s }: { s: ServerView }) {
  return !s.last_heartbeat ? <StatusBadge kind="off">Never connected</StatusBadge> : s.offline ? <StatusBadge kind="off">Offline</StatusBadge> : <StatusBadge kind="ok">Online</StatusBadge>;
}

export const specs = (s: ServerView) =>
  [s.cores && `${s.cores} ${s.cores === 1 ? "core" : "cores"}`, s.mem_total && bytes(s.mem_total)].filter(Boolean).join(", ");

const DISK_WARN_DAYS = 30;

export function attention(s: ServerView): { text: string; bad: boolean }[] {
  const days = s.forecast?.growing ? s.forecast.days : null;
  return [
    s.offline && { text: s.last_heartbeat ? `Offline, seen ${ago(s.last_heartbeat)}` : "Offline", bad: true },
    s.firewall_off && { text: "Firewall off", bad: false },
    days !== null && days !== undefined && days < DISK_WARN_DAYS && { text: `Disk full in ${days} days`, bad: days <= 7 },
  ].filter((c) => !!c);
}

export function ServerRow({ s }: { s: ServerView }) {
  const course = s.forecast && forecastLine(s.disk_used_pct, s.forecast);
  return (
    <tr>
      <td>
        <Link to={`/servers/${encodeURIComponent(s.name)}`}><b>{s.name}</b></Link><div className="dim mono">{s.public_ip}</div>
        {specs(s) && <div className="dim">{specs(s)}</div>}
      </td>
      <td><ServerState s={s} /></td>
      <td>{ago(s.last_heartbeat)}</td>
      <td>
        <div className="row" style={{ gap: 8 }}><Bar pct={s.disk_used_pct} /><span className="num">{s.disk_used_pct}%</span></div>
        {course && course[1] && <div className="bad" style={{ color: "var(--bad)" }}>{course[0]}</div>}
      </td>
      <td><div className="row" style={{ gap: 6, flexWrap: "wrap" }}>{attention(s).map((c) => <span key={c.text} className={"tag" + (c.bad ? " bad" : "")}>{c.text}</span>)}</div></td>
    </tr>
  );
}

export function Servers() {
  const list = useLoad<ServerView[]>("/api/servers", POLL_MS);
  const managed = useContext(SessionContext).managed;
  const tbl = useTable(list.data ?? [], { text: (s) => [s.name, s.public_ip, s.private_ip ?? ""].join(" "),
    sort: { name: (s) => s.name, seen: (s) => s.last_heartbeat ?? 0, disk: (s) => s.disk_used_pct } });
  return (
    <Shell crumb={["Servers"]} active="/servers">
      <div className="ph">
        <div><h1>Servers</h1><p className="sub">Each server runs one agent. The agent dials out to this panel, so no management port is open on it.</p></div>
        <Link to="/servers/new" className="btn pri"><Icon name="plus" />Add server</Link>
      </div>
      {!list.data ? <Loading what={list} /> : (
        <>
          {list.data.length === 1 && !managed && (
            <Callout tone="warn">This machine watches itself. If it goes down, no message can be sent. A second machine fixes this in a later version.</Callout>
          )}
          {list.data.length === 0 ? (managed ? (
            <div className="col"><h2>Add your first server</h2><p className="muted">{MANAGED_NEEDS}</p><JoinCommand intro="Run the command on it; the server appears here within a minute." /></div>
          ) : <Empty text="No server has joined yet."><Link to="/servers/new" className="btn pri">Add server</Link></Empty>) : (
            <div className="col">
              <TableBar t={tbl} noun="servers" find="Search servers" />
              <div className="card scroll">
                <table className="tbl">
                  <thead><tr><SortTh t={tbl} k="name">Server</SortTh><th>Agent</th><SortTh t={tbl} k="seen">Last seen</SortTh><SortTh t={tbl} k="disk">Disk</SortTh><th>Needs attention</th></tr></thead>
                  <tbody>
                    {tbl.rows.map((s) => <ServerRow key={s.id} s={s} />)}
                  </tbody>
                </table>
              </div>
              <Pager page={tbl.page} pages={tbl.pages} onPage={tbl.setPage} />
            </div>
          )}
        </>
      )}
    </Shell>
  );
}
