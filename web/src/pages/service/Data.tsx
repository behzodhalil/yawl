import { useEffect, useState } from "react";
import { api } from "../../api";
import { Pager } from "../../table";
import { Callout, Empty, Modal, Skeleton, errText, useLoad } from "../../ui";

type Table = { schema: string; name: string };
type Result = { columns: string[] | null; rows: (string | null)[][] | null; more?: boolean; changed?: number };

const tableName = (t: Table) => (t.schema && t.schema !== "public" ? `${t.schema}.${t.name}` : t.name);

export function Data({ project, env, service }: { project: string; env: string; service: string }) {
  const path = `/api/services/${project}/${env}/${service}/data`;
  const me = useLoad<{ role: string }>("/api/me");
  const [tables, setTables] = useState<Table[] | null>(null);
  const [listErr, setListErr] = useState("");
  const [open, setOpen] = useState<{ table: Table; page: number } | null>(null);
  const [sql, setSql] = useState("");
  const [writes, setWrites] = useState(false);
  const [asking, setAsking] = useState(false);
  const [busy, setBusy] = useState(false);
  const [shown, setShown] = useState<Result | null>(null);
  const [err, setErr] = useState("");

  useEffect(() => {
    api<{ tables: Table[] }>("POST", path + "/tables").then((d) => setTables(d.tables), (e) => setListErr(errText(e)));
  }, [path]);

  async function show(call: () => Promise<Result>) {
    setErr(""), setBusy(true);
    try { setShown(await call()); } catch (e) { setErr(errText(e)), setShown(null); } finally { setBusy(false); }
  }
  const browse = (table: Table, page: number) => {
    setOpen({ table, page });
    show(() => api("POST", path + "/rows", { schema: table.schema, table: table.name, page }));
  };
  const run = (write: boolean) => {
    setAsking(false), setOpen(null);
    show(() => api("POST", path + (write ? "/write" : "/query"), { sql }));
  };
  const onRun = () => (writes ? setAsking(true) : run(false));

  return (
    <div className="datagrid">
      <table className="tbl">
        <thead><tr><th>Tables</th></tr></thead>
        <tbody>
          {tables?.map((t) => (
            <tr key={t.schema + "." + t.name} className="click" aria-selected={open?.table === t}>
              <td className="mono" style={{ fontWeight: open?.table === t ? 600 : undefined, wordBreak: "break-all" }}>
                <button type="button" className="tblname" onClick={() => browse(t, 0)}>{tableName(t)}</button>
              </td>
            </tr>
          ))}
          {tables === null && !listErr && <tr><td><Skeleton /></td></tr>}
        </tbody>
      </table>
      <div className="col" style={{ minWidth: 0 }}>
        {listErr && <Callout tone="bad">{listErr}</Callout>}
        {tables?.length === 0 && <Empty text="No tables yet." />}
        <textarea aria-label="SQL" className="in mono" rows={4} style={{ height: "auto", padding: 8 }} spellCheck={false} placeholder="select * from users limit 10" value={sql}
          onChange={(e) => setSql(e.target.value)} onKeyDown={(e) => { if (e.key === "Enter" && (e.metaKey || e.ctrlKey) && sql.trim()) { e.preventDefault(); onRun(); } }} />
        <div className="row">
          <button className="btn pri" disabled={!sql.trim() || busy} onClick={onRun}>Run</button>
          {!sql.trim() && <span className="muted">A query is needed</span>}
          {me.data?.role === "admin" && (
            <label className="row" style={{ gap: 6 }}><input type="checkbox" checked={writes} onChange={(e) => setWrites(e.target.checked)} />Writes</label>
          )}
        </div>
        {err && <Callout tone="bad"><span className="mono" style={{ whiteSpace: "pre-wrap" }}>{err}</span></Callout>}
        {busy && !shown && <Skeleton />}
        {shown && <Rows title={open ? tableName(open.table) : ""} r={shown} />}
        {shown && open && <Pager page={open.page} more={!!shown.more} onPage={(p) => browse(open.table, p)} />}
      </div>
      {asking && (
        <Modal title={`Run with writes on ${service}?`} onClose={() => setAsking(false)}>
          <pre className="in mono" style={{ height: "auto", padding: 8, margin: 0, whiteSpace: "pre-wrap", maxHeight: 240, overflow: "auto" }}>{sql}</pre>
          <p>This can change or delete data in {service}. Yawl keeps no copy of what it changes; the last backup is your way back.</p>
          <div className="row">
            <button className="btn danger" onClick={() => run(true)}>Run with writes</button>
            <button className="btn ghost" onClick={() => setAsking(false)}>Cancel</button>
          </div>
        </Modal>
      )}
    </div>
  );
}

function Rows({ title, r }: { title: string; r: Result }) {
  if (r.changed !== undefined) return <p>{`Done. ${r.changed} ${r.changed === 1 ? "row" : "rows"} changed.`}</p>;
  const rows = r.rows ?? [];
  const count = r.more && !title ? `Showing the first ${rows.length} rows` : `${rows.length} ${rows.length === 1 ? "row" : "rows"}`;
  return (
    <div className="col" style={{ gap: 8 }}>
      <p className="muted">{title ? `${title} · ${count}` : count}</p>
      {rows.length > 0 && (
        <div style={{ overflowX: "auto" }}>
          <table className="tbl">
            <thead><tr>{(r.columns ?? []).map((c, i) => <th key={i}>{c}</th>)}</tr></thead>
            <tbody>
              {rows.map((row, i) => (
                <tr key={i}>
                  {row.map((v, j) => (
                    <td key={j} className="mono" title={v ?? "NULL"} style={{ maxWidth: 320, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
                      {v === null ? <span className="dim">NULL</span> : v}
                    </td>
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
