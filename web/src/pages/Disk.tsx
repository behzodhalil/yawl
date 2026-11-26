import { useState } from "react";
import { api } from "../api";
import { ago, bytes, dateTime, hhmm } from "../format";
import { zoneName } from "../LogView";
import { Shell } from "../Shell";
import { ServerTabs } from "./ServerTabs";
import { Button, LayerCard, Table } from "@cloudflare/kumo";
import { Callout, Link, Loading, Modal, errText, useLoad, Card } from "../ui";

const DAILY_UTC_HOUR = 4;

const dailyLocal = () => hhmm(new Date().setUTCHours(DAILY_UTC_HOUR, 0, 0, 0));

type Run = { at: number; why: string; containers?: number; images?: number; freed: number; cause?: string };

export function Disk({ name }: { name: string }) {
  const base = `/api/servers/${encodeURIComponent(name)}`;
  const last = useLoad<Run>(`${base}/cleanup`);
  const unused = useLoad<string[]>(`${base}/volumes/unused`);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const [removing, setRemoving] = useState("");
  async function now() {
    setErr(""), setBusy(true);
    try { await api("POST", `${base}/cleanup`); last.reload(); } catch (e) { setErr(errText(e)); } finally { setBusy(false); }
  }
  async function remove() {
    try { await api("DELETE", `${base}/volumes/${encodeURIComponent(removing)}`); setRemoving(""); unused.reload(); } catch (e) { setErr(errText(e)); setRemoving(""); }
  }
  const r = last.data;
  return (
    <Shell crumb={[<Link to="/servers">Servers</Link>, name, "Disk"]} active="/servers">
      <ServerTabs name={name} active="disk" />
      <p className="text-kumo-subtle">{`Every day at ${dailyLocal()} (${zoneName(false)}), and as soon as the disk is 80% full, Yawl removes its stopped containers, unused untagged images and old build cache.`}</p>
      {err && <Callout tone="bad">{err}</Callout>}
      <Card title="Cleanup" className="max-w-[var(--w-form)]">
        {!r ? <Loading what={last} /> : !r.at ? <p className="text-kumo-subtle">No cleanup has run yet.</p> : r.cause
          ? <Callout tone="bad">{`The last cleanup (${r.why}, ${ago(r.at)}) failed: ${r.cause}`}</Callout>
          : <p title={dateTime(r.at)}>{`Freed ${bytes(r.freed)}: ${r.containers ?? 0} containers, ${r.images ?? 0} images, and build cache. ${r.why === "by hand" ? "Run by hand" : r.why === "disk" ? "The disk was full" : "Daily"}, ${ago(r.at)}.`}</p>}
        <Button className="self-start" disabled={busy} onClick={now}>Clean up now</Button>
      </Card>
      <Card title="Unused volumes" className="max-w-[var(--w-form)]">
        <p className="text-kumo-subtle">Yawl volumes no container uses and no service owns, not even one in the trash. A cleanup never removes a volume; remove one here when you know its data is not needed.</p>
        {!unused.data ? <Loading what={unused} /> : unused.data.length === 0 ? <p className="text-kumo-subtle">None.</p> : (
          <LayerCard className="overflow-x-auto p-0">
            <Table>
              <Table.Body>
                {unused.data.map((v) => (
                  <Table.Row key={v}><Table.Cell className="font-mono">{v}</Table.Cell><Table.Cell className="text-right"><Button size="sm" variant="ghost" className="text-kumo-danger" aria-label={`Remove ${v}`} onClick={() => setRemoving(v)}>Remove</Button></Table.Cell></Table.Row>
                ))}
              </Table.Body>
            </Table>
          </LayerCard>
        )}
      </Card>
      {removing && (
        <Modal title={`Remove ${removing}?`} onClose={() => setRemoving("")}>
          <p>Its data is deleted from the server and cannot be brought back.</p>
          <div className="flex flex-wrap items-center gap-2.5">
            <Button variant="destructive" onClick={remove}>Remove the volume</Button>
            <Button variant="ghost" onClick={() => setRemoving("")}>Cancel</Button>
          </div>
        </Modal>
      )}
    </Shell>
  );
}
