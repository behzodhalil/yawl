import { useRef, useState } from "react";
import { api } from "../../api";
import { ago, backupWord, dateTime } from "../../format";
import { decodeRecovery, sealTo } from "../../seal";
import type { Volume } from "../../types";
import { Ago, Callout, Field, Link, Loading, Modal, StatusBadge, errText, useBusy, useLiveLoad, useLoad } from "../../ui";
import { REF_KEY } from "./DbInfo";
import { ImportDump } from "./Import";

type Backup = { id: string; state: string; size?: number; cause?: string; created_at: number; finished_at?: number; volume?: string;
  copies?: { copy: string; state: string; cause?: string }[] };

export function size(n?: number): string {
  if (!n) return "";
  const u = ["B", "KB", "MB", "GB", "TB"];
  let i = 0, x = n;
  while (x >= 1024 && i < u.length - 1) (x /= 1024), i++;
  return `${x.toFixed(i ? 1 : 0)} ${u[i]}`;
}

type Keep = { daily: number; weekly: number; monthly: number };

export function kept(k?: Keep): string {
  const r = k ?? { daily: 7, weekly: 4, monthly: 0 };
  const parts = [`${r.daily} daily`, r.weekly ? `${r.weekly} weekly` : "", r.monthly ? `${r.monthly} monthly` : ""].filter(Boolean);
  return parts.length > 1 ? `${parts.slice(0, -1).join(", ")} and ${parts[parts.length - 1]}` : parts[0];
}


async function sealKey(path: string, key: string): Promise<string> {
  await decodeRecovery(key);
  const keys = await api<{ agent_pubkey: string; server: string }>("GET", path + "/keys");
  if (!keys.agent_pubkey) throw new Error(`Server ${keys.server} has no secrets key; join it again.`);
  return sealTo(keys.agent_pubkey, new TextEncoder().encode(key.trim()));
}

function ReadFrom({ b, value, onChange }: { b: Backup; value: string; onChange: (v: string) => void }) {
  const ok = (b.copies ?? []).filter((c) => c.state === "ok");
  if (ok.length === 0) return null;
  return (
    <Field label="Read from">{(id) => (
      <select id={id} className="in" value={value} onChange={(e) => onChange(e.target.value)}>
        <option value="">Main storage</option>
        {ok.map((c) => <option key={c.copy} value={c.copy}>{`Copy ${c.copy}`}</option>)}
      </select>
    )}</Field>
  );
}

function BackupRows({ rows, onRestore }: { rows: Backup[]; onRestore: (b: Backup) => void }) {
  return (
    <div className="card scroll">
      <table className="tbl">
        <thead><tr><th>Backup</th><th>State</th><th>Size</th><th>Taken</th><th /></tr></thead>
        <tbody>
          {rows.map((b) => (
            <tr key={b.id}>
              <td className="mono">{b.id}</td>
              <td><div className="col" style={{ gap: 4 }}><StatusBadge kind={backupWord(b.state)[1]}>{backupWord(b.state)[0]}</StatusBadge>{b.cause && <span className="muted">{b.cause}</span>}
                {b.copies?.filter((c) => c.state !== "ok").map((c) => <span key={c.copy} className="muted">{`Copy to ${c.copy} failed: ${c.cause ?? ""}`}</span>)}</div></td>
              <td className="num">{size(b.size)}</td>
              <td><Ago ms={b.created_at} /></td>
              <td>{b.state === "ok" && <button className="btn sm" aria-label={`Restore ${b.id}`} onClick={() => onRestore(b)}>Restore</button>}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

export function Backups({ project, env, service }: { project: string; env: string; service: string }) {
  const path = `/api/services/${project}/${env}/${service}`;
  const list = useLiveLoad<Backup[]>(path + "/backups", (l) => l.some((b) => b.state === "running"));
  const view = useLoad<{ form: { kind: string; backup?: boolean; keep?: Keep } }>(path);
  const nightly = !view.data || view.data.form.kind === "postgres" || view.data.form.backup !== false;
  const [err, setErr] = useState("");
  const [, run] = useBusy();
  const pitr = useLoad<{ pitr: boolean; from: number; to: number; shipped: number }>(path + "/pitr");
  const [restoring, setRestoring] = useState<Backup | "time" | null>(null);
  const [at, setAt] = useState("");
  const [into, setInto] = useState("");
  const [source, setSource] = useState("");
  const [key, setKey] = useState("");
  const [rErr, setRErr] = useState("");
  const [done, setDone] = useState<{ into: string; deploy: string } | null>(null);
  const w = pitr.data;

  const restore = () => run(async () => {
    setRErr("");
    try {
      const box = await sealKey(path, key);
      const from = restoring === "time" ? { at: (at.length === 16 ? at + ":00" : at) + "Z" } : { backup: restoring!.id, ...(source ? { from: source } : {}) };
      const out = await api<{ deploy: string }>("POST", path + "/restore", { ...from, into, recovery_box: box });
      setKey("");
      setRestoring(null);
      setDone({ into, deploy: out.deploy });
    } catch (e) {
      setRErr(errText(e));
    }
  });

  return (
    <div className="col">
      <div className="row" style={{ justifyContent: "space-between" }}>
        <h2>Backups</h2>
        <button className="btn pri" onClick={() => run(async () => {
          try { await api("POST", path + "/backups"); list.reload(); } catch (e) { setErr(errText(e)); }
        })}>Back up now</button>
      </div>
      <p className="muted">{nightly ? `Daily, encrypted to your recovery key, kept off this server: ${kept(view.data?.form.keep)}.`
        : "Not backed up every night. Turn it on with backup: true in yawl.yaml; Back up now still works."}</p>
      {view.data?.form.kind === "clickhouse" && (
        <p className="muted">A materialized view without a TO table comes back with its definition but without the rows it had collected.</p>
      )}
      {err && <Callout tone="bad">{err}</Callout>}
      {w?.pitr && (
        <div className="row" style={{ justifyContent: "space-between" }}>
          <p>{w.from ? `Any second from ${dateTime(w.from)} to ${dateTime(w.to)}.` : "Point in time recovery starts with the next nightly backup."}</p>
          {w.from > 0 && <button className="btn" onClick={() => (setRestoring("time"), setInto(`${service}-restored`))}>Restore to a time</button>}
        </div>
      )}
      {w?.pitr && w.shipped > 0 && Date.now() - w.shipped > 10 * 60_000 && (
        <Callout tone="warn">{`Changes were last uploaded ${ago(w.shipped)}; a restore reaches only that far. Check the backup storage.`}</Callout>
      )}
      {done && (
        <Callout tone="ok" title={`Restoring into ${done.into}.`}>
          Once its deploy is live, point an app at it: set {REF_KEY[view.data?.form.kind ?? ""] ?? "DATABASE_URL"} to the reference {done.into}.url, then deploy.{" "}
          <Link to={`/p/${project}/${env}/s/${done.into}/deploys/${done.deploy}`}>Follow the restore</Link>
        </Callout>
      )}
      {!list.data ? <Loading what={list} /> : list.data.length === 0 ? <p className="muted">No backups yet.</p> : (
        <BackupRows rows={list.data} onRestore={(b) => (setRestoring(b), setInto(`${service}-restored`), setSource(""))} />
      )}
      {restoring && (
        <Modal title={restoring === "time" ? "Restore to a time" : `Restore ${restoring.id}`} onClose={() => setRestoring(null)}>
          <p className="muted">A restore always goes into a new database. The live one is not touched.</p>
          {restoring === "time" && <Field label="Time (UTC)">{(id) => <input id={id} className="in mono" type="datetime-local" step={1} value={at} onChange={(e) => setAt(e.target.value)} />}</Field>}
          <Field label="New database name">{(id) => <input id={id} className="in" value={into} onChange={(e) => setInto(e.target.value)} />}</Field>
          {restoring !== "time" && <ReadFrom b={restoring} value={source} onChange={setSource} />}
          <Field label="Recovery key" hint="Sealed in this browser to the agent that restores. The server never sees it.">
            {(id) => <textarea id={id} className="in mono" rows={2} style={{ height: "auto", padding: 8 }} autoComplete="off" spellCheck={false} value={key} onChange={(e) => setKey(e.target.value)} />}
          </Field>
          {rErr && <Callout tone="bad">{rErr}</Callout>}
          <div className="row">
            <button className="btn pri" onClick={restore}>Restore into a new database</button>
            <button className="btn ghost" onClick={() => setRestoring(null)}>Cancel</button>
          </div>
        </Modal>
      )}
      {view.data && !["valkey", "clickhouse"].includes(view.data.form.kind) && <ImportDump project={project} env={env} service={service} />}
    </div>
  );
}

export function VolumeBackups({ project, env, service, volumes }: { project: string; env: string; service: string; volumes: Volume[] }) {
  const path = `/api/services/${project}/${env}/${service}`;
  const list = useLiveLoad<Backup[]>(path + "/backups", (l) => l.some((b) => b.state === "running"));
  const view = useLoad<{ form: { keep?: Keep } }>(path);
  const [err, setErr] = useState("");
  const [, run] = useBusy();
  const going = useRef(new Set<string>());
  const [backing, setBacking] = useState<string[]>([]);
  const [restoring, setRestoring] = useState<Backup | null>(null);
  const [source, setSource] = useState("");
  const [key, setKey] = useState("");
  const [typed, setTyped] = useState("");
  const [rErr, setRErr] = useState("");
  const [done, setDone] = useState<{ volume: string; deploy: string } | null>(null);

  const restore = () => run(async () => {
    setRErr("");
    try {
      const box = await sealKey(path, key);
      const out = await api<{ deploy: string }>("POST", path + "/restore", { backup: restoring!.id, ...(source ? { from: source } : {}), recovery_box: box });
      setKey("");
      setDone({ volume: restoring!.volume!, deploy: out.deploy });
      setRestoring(null);
    } catch (e) {
      setRErr(errText(e));
    }
  });

  async function backUp(volume: string) {
    if (going.current.has(volume)) return;
    going.current.add(volume);
    setBacking([...going.current]);
    try { await api("POST", path + "/backups", { volume }); list.reload(); } catch (e) { setErr(errText(e)); }
    going.current.delete(volume);
    setBacking([...going.current]);
  }

  return (
    <div className="col">
      <h2>Backups</h2>
      <p className="muted">{`Each volume nightly, encrypted to your recovery key, kept off this server: ${kept(view.data?.form.keep)}.`}</p>
      <Callout tone="warn">A volume is read while the app runs: a file being written at that moment may be cut. Keep what must be exact in Postgres.</Callout>
      {err && <Callout tone="bad">{err}</Callout>}
      {done && (
        <Callout tone="ok" title={`Restoring ${done.volume}.`}>
          <Link to={`/p/${project}/${env}/s/${service}/deploys/${done.deploy}`}>Follow the restore</Link>
        </Callout>
      )}
      {!list.data ? <Loading what={list} /> : volumes.map((v) => {
        const rows = list.data!.filter((b) => b.volume === v.name);
        return (
          <section key={v.name} className="col">
            <div className="row" style={{ justifyContent: "space-between" }}>
              <h3>{v.name} <span className="muted mono">{v.path}</span></h3>
              <button className="btn" aria-label={`Back up ${v.name} now`} disabled={backing.includes(v.name)} onClick={() => backUp(v.name)}>Back up now</button>
            </div>
            {rows.length === 0 ? <p className="muted">No backups of {v.name} yet.</p> : <BackupRows rows={rows} onRestore={(b) => (setRestoring(b), setTyped(""), setSource(""))} />}
          </section>
        );
      })}
      {restoring && (
        <Modal title={`Restore ${restoring.volume}`} onClose={() => setRestoring(null)}>
          <p>{service} stops, {restoring.volume} is emptied and filled from the backup of {dateTime(restoring.finished_at || restoring.created_at)}, then {service} starts.</p>
          <ReadFrom b={restoring} value={source} onChange={setSource} />
          <Field label="Recovery key" hint="Sealed in this browser to the agent that restores. The server never sees it.">
            {(id) => <textarea id={id} className="in mono" rows={2} style={{ height: "auto", padding: 8 }} autoComplete="off" spellCheck={false} value={key} onChange={(e) => setKey(e.target.value)} />}
          </Field>
          <Field label={`Type ${service} to confirm`}>{(id) => <input id={id} className="in mono" autoComplete="off" value={typed} onChange={(e) => setTyped(e.target.value)} />}</Field>
          {rErr && <Callout tone="bad">{rErr}</Callout>}
          <div className="row">
            <button className="btn danger" disabled={typed !== service} onClick={restore}>Restore {restoring.volume}</button>
            <button className="btn ghost" onClick={() => setRestoring(null)}>Cancel</button>
          </div>
        </Modal>
      )}
    </div>
  );
}
