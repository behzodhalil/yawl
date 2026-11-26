import { useState } from "react";
import { Badge, Button } from "@cloudflare/kumo";
import { api } from "../../api";
import { dateTime } from "../../format";
import { Callout, Loading, errText, useLoad, Card } from "../../ui";

type Item = { kind: string; id: string; label: string; deleted_by: string; deleted_at: number; purge_at: number; waiting?: string };

export function Trash() {
  const list = useLoad<Item[]>("/api/trash");
  const [err, setErr] = useState("");
  if (!list.data) return <Loading what={list} />;
  return (
    <Card title="Trash" className="max-w-[var(--w-form)]">
      <p className="text-kumo-subtle">Deleted things stop at once and wait here 48 hours. Restore brings back their variables and data; after that they are gone, backups too.</p>
      {list.data.length === 0 ? <p className="text-kumo-subtle">Nothing is in the trash.</p> : list.data.map((i) => (
        <div key={i.kind + i.id} className="flex flex-wrap items-start justify-between gap-2.5">
          <div className="flex flex-col gap-0.5">
            <span><b>{i.label}</b> <Badge variant="outline">{i.kind}</Badge></span>
            <span className="text-kumo-subtle">{`Deleted by ${i.deleted_by} on ${dateTime(i.deleted_at)}; purged on ${dateTime(i.purge_at)}.`}</span>
            {i.waiting && <span className="text-kumo-warning">{i.waiting}</span>}
          </div>
          {i.purge_at <= Date.now() ? <span className="text-kumo-subtle">being purged</span> : <Button size="sm" onClick={async () => {
            setErr("");
            try { await api("POST", `/api/trash/${i.kind}/${i.id}/restore`); list.reload(); } catch (e) { setErr(errText(e)); }
          }}>Restore</Button>}
        </div>
      ))}
      {err && <Callout tone="bad">{err}</Callout>}
    </Card>
  );
}
