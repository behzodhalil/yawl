import { useEffect, useState } from "react";
import { api } from "../../api";
import { Pager } from "../../table";
import { Button, LayerCard, Table as KumoTable, Textarea, cn, Switch } from "@cloudflare/kumo";
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
    <div className="grid items-start gap-5 md:grid-cols-[minmax(160px,220px)_minmax(0,1fr)]">
      <LayerCard className="overflow-x-auto p-0">
        <KumoTable>
          <KumoTable.Header><KumoTable.Row><KumoTable.Head>Tables</KumoTable.Head></KumoTable.Row></KumoTable.Header>
          <KumoTable.Body>
            {tables?.map((t) => (
              <KumoTable.Row key={t.schema + "." + t.name} aria-selected={open?.table === t} className="hover:bg-kumo-tint">
                <KumoTable.Cell className={cn("font-mono text-sm break-all", open?.table === t && "font-semibold")}>
                  <button type="button" className="block w-full cursor-pointer text-left" onClick={() => browse(t, 0)}>{tableName(t)}</button>
                </KumoTable.Cell>
              </KumoTable.Row>
            ))}
            {tables === null && !listErr && <KumoTable.Row><KumoTable.Cell><Skeleton /></KumoTable.Cell></KumoTable.Row>}
          </KumoTable.Body>
        </KumoTable>
      </LayerCard>
      <div className="flex min-w-0 flex-col gap-3">
        {listErr && <Callout tone="bad">{listErr}</Callout>}
        {tables?.length === 0 && <Empty text="No tables yet." />}
        <Textarea aria-label="SQL" className="w-full font-mono text-sm" rows={4} spellCheck={false} placeholder="select * from users limit 10" value={sql}
          onChange={(e) => setSql(e.target.value)} onKeyDown={(e) => { if (e.key === "Enter" && (e.metaKey || e.ctrlKey) && sql.trim()) { e.preventDefault(); onRun(); } }} />
        <div className="flex flex-wrap items-center gap-2.5">
          <Button variant="primary" disabled={!sql.trim() || busy} onClick={onRun}>Run</Button>
          {!sql.trim() && <span className="text-kumo-subtle">A query is needed</span>}
          {me.data?.role === "admin" && (
            <Switch label="Writes" size="sm" checked={writes} onClick={() => setWrites(!writes)} />
          )}
        </div>
        {err && <Callout tone="bad"><span className="font-mono whitespace-pre-wrap">{err}</span></Callout>}
        {busy && !shown && <Skeleton />}
        {shown && <Rows title={open ? tableName(open.table) : ""} r={shown} />}
        {shown && open && <Pager page={open.page} more={!!shown.more} onPage={(p) => browse(open.table, p)} />}
      </div>
      {asking && (
        <Modal title={`Run with writes on ${service}?`} onClose={() => setAsking(false)}>
          <pre className="m-0 max-h-60 overflow-auto whitespace-pre-wrap rounded-lg bg-kumo-recessed p-2 font-mono text-sm">{sql}</pre>
          <p>This can change or delete data in {service}. Yawl keeps no copy of what it changes; the last backup is your way back.</p>
          <div className="flex flex-wrap items-center gap-2.5">
            <Button variant="destructive" onClick={() => run(true)}>Run with writes</Button>
            <Button variant="ghost" onClick={() => setAsking(false)}>Cancel</Button>
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
    <div className="flex flex-col gap-2">
      <p className="text-kumo-subtle">{title ? `${title} · ${count}` : count}</p>
      {rows.length > 0 && (
        <LayerCard className="overflow-x-auto p-0">
          <KumoTable>
            <KumoTable.Header><KumoTable.Row>{(r.columns ?? []).map((c, i) => <KumoTable.Head key={i}>{c}</KumoTable.Head>)}</KumoTable.Row></KumoTable.Header>
            <KumoTable.Body>
              {rows.map((row, i) => (
                <KumoTable.Row key={i}>
                  {row.map((v, j) => (
                    <KumoTable.Cell key={j} title={v ?? "NULL"} className="max-w-80 truncate font-mono text-sm">
                      {v === null ? <span className="text-kumo-subtle">NULL</span> : v}
                    </KumoTable.Cell>
                  ))}
                </KumoTable.Row>
              ))}
            </KumoTable.Body>
          </KumoTable>
        </LayerCard>
      )}
    </div>
  );
}
