import { useState } from "react";
import { api } from "../api";
import { ago, bytes, dateTime } from "../format";
import { Shell } from "../Shell";
import { ServerTabs } from "./ServerTabs";
import { Callout, Link, Loading, Modal, errText, useLoad } from "../ui";

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
      <h1>{name}</h1>
      <ServerTabs name={name} active="disk" />
      <p className="sub">Every night at 04:00 UTC, and as soon as the disk is 80% full, Yawl removes its stopped containers, unused untagged images and old build cache.</p>
      {err && <Callout tone="bad">{err}</Callout>}
      <div className="card pad col w-form">
        <h2>Cleanup</h2>
        {!r ? <Loading what={last} /> : !r.at ? <p className="muted">No cleanup has run yet.</p> : r.cause
          ? <Callout tone="bad">{`The last cleanup (${r.why}, ${ago(r.at)}) failed: ${r.cause}`}</Callout>
          : <p title={dateTime(r.at)}>{`Freed ${bytes(r.freed)}: ${r.containers ?? 0} containers, ${r.images ?? 0} images, and build cache. ${r.why === "by hand" ? "Run by hand" : r.why === "disk" ? "The disk was full" : "Nightly"}, ${ago(r.at)}.`}</p>}
        <div><button className="btn" disabled={busy} onClick={now}>Clean up now</button></div>
      </div>
      <div className="card pad col w-form">
        <h2>Unused volumes</h2>
        <p className="muted">Yawl volumes no container uses and no service owns, not even one in the trash. A cleanup never removes a volume; remove one here when you know its data is not needed.</p>
        {!unused.data ? <Loading what={unused} /> : unused.data.length === 0 ? <p className="muted">None.</p> : (
          <div className="card scroll">
            <table className="tbl">
              <tbody>
                {unused.data.map((v) => (
                  <tr key={v}><td className="mono">{v}</td><td><button className="btn sm danger" aria-label={`Remove ${v}`} onClick={() => setRemoving(v)}>Remove</button></td></tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>
      {removing && (
        <Modal title={`Remove ${removing}?`} onClose={() => setRemoving("")}>
          <p>Its data is deleted from the server and cannot be brought back.</p>
          <div className="row">
            <button className="btn danger" onClick={remove}>Remove the volume</button>
            <button className="btn ghost" onClick={() => setRemoving("")}>Cancel</button>
          </div>
        </Modal>
      )}
    </Shell>
  );
}
