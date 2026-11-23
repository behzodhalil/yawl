import { useState } from "react";
import { api } from "../../api";
import { dateTime } from "../../format";
import { Callout, Loading, errText, useLoad } from "../../ui";

type Item = { kind: string; id: string; label: string; deleted_by: string; deleted_at: number; purge_at: number; waiting?: string };

export function Trash() {
  const list = useLoad<Item[]>("/api/trash");
  const [err, setErr] = useState("");
  if (!list.data) return <Loading what={list} />;
  return (
    <div className="card pad col" style={{ maxWidth: 720 }}>
      <h2>Trash</h2>
      <p className="muted">Deleted things stop at once and wait here 48 hours. Restore brings back their variables and data; after that they are gone, backups too.</p>
      {list.data.length === 0 ? <p className="muted">Nothing is in the trash.</p> : list.data.map((i) => (
        <div key={i.kind + i.id} className="row" style={{ justifyContent: "space-between", alignItems: "flex-start" }}>
          <div className="col" style={{ gap: 2 }}>
            <span><b>{i.label}</b> <span className="tag">{i.kind}</span></span>
            <span className="muted">{`Deleted by ${i.deleted_by} on ${dateTime(i.deleted_at)}; purged on ${dateTime(i.purge_at)}.`}</span>
            {i.waiting && <span style={{ color: "var(--warn)" }}>{i.waiting}</span>}
          </div>
          {i.purge_at <= Date.now() ? <span className="muted">being purged</span> : <button className="btn sm" onClick={async () => {
            setErr("");
            try { await api("POST", `/api/trash/${i.kind}/${i.id}/restore`); list.reload(); } catch (e) { setErr(errText(e)); }
          }}>Restore</button>}
        </div>
      ))}
      {err && <Callout tone="bad">{err}</Callout>}
    </div>
  );
}
