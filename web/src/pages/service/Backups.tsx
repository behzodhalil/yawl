import { useRef, useState } from "react";
import { api } from "../../api";
import { ago, backupWord, bytes, dateTime, dayTime } from "../../format";
import { decodeRecovery, sealTo } from "../../seal";
import type { Volume } from "../../types";
import { Button, LayerCard, Table, SensitiveInput, linkVariants, Radio } from "@cloudflare/kumo";
import { Callout, Field, Link, Loading, Modal, StatusBadge, errText, useBusy, useLiveLoad, useLoad, Input } from "../../ui";
import { REF_KEY } from "./DbInfo";
import { ImportDump } from "./Import";

type Backup = { id: string; state: string; size?: number; cause?: string; created_at: number; finished_at?: number; volume?: string;
  copies?: { copy: string; state: string; cause?: string }[] };

export const size = (n?: number) => (n ? bytes(n) : "");

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
    <Radio.Group legend="Read from" orientation="horizontal" value={value} onValueChange={(v: string) => onChange(v)}>
      <Radio.Item label="Main storage" value="" />
      {ok.map((c) => <Radio.Item key={c.copy} label={`Copy ${c.copy}`} value={c.copy} />)}
    </Radio.Group>
  );
}

const NO_STORAGE = /\s*backup storage is not set: run yawl server storage set\.?/i;

export const saysNoStorage = (text: string) => NO_STORAGE.test(text);

export function Cause({ text, muted = true }: { text: string; muted?: boolean }) {
  const className = muted ? "text-kumo-subtle" : undefined;
  if (!saysNoStorage(text)) return <span className={className}>{text}</span>;
  const rest = text.replace(NO_STORAGE, "").trim();
  return <span className={className}>{rest && `${rest} `}Backup storage is not set. <Link className={linkVariants({ variant: "inline" })} to="/settings/storage">Set up storage</Link></span>;
}

function BackupRows({ rows, onRestore }: { rows: Backup[]; onRestore: (b: Backup) => void }) {
  return (
    <LayerCard className="overflow-x-auto p-0">
      <Table>
        <Table.Header><Table.Row><Table.Head>Taken</Table.Head><Table.Head>State</Table.Head><Table.Head>Size</Table.Head><Table.Head /></Table.Row></Table.Header>
        <Table.Body>
          {rows.map((b) => (
            <Table.Row key={b.id}>
              <Table.Cell title={`${dateTime(b.created_at)} · ${b.id}`}>{dayTime(b.created_at)}</Table.Cell>
              <Table.Cell><div className="flex flex-col gap-1"><StatusBadge kind={backupWord(b.state)[1]}>{backupWord(b.state)[0]}</StatusBadge>{b.cause && <Cause text={b.cause} />}
                {b.copies?.filter((c) => c.state !== "ok").map((c) => <span key={c.copy} className="text-kumo-subtle">{`Copy to ${c.copy} failed: ${c.cause ?? ""}`}</span>)}</div></Table.Cell>
              <Table.Cell className="font-mono text-sm tabular-nums">{size(b.size)}</Table.Cell>
              <Table.Cell>{b.state === "ok" && <Button size="sm" aria-label={`Restore the ${b.volume ? `${b.volume} ` : ""}backup of ${dateTime(b.created_at)}${b.size ? `, ${size(b.size)}` : ""}`} onClick={() => onRestore(b)}>Restore</Button>}</Table.Cell>
            </Table.Row>
          ))}
        </Table.Body>
      </Table>
    </LayerCard>
  );
}

function utcSecond(local: string): string {
  const t = new Date(local);
  return isNaN(t.getTime()) ? local : t.toISOString().slice(0, 19) + "Z";
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
      const from = restoring === "time" ? { at: utcSecond(at) } : { backup: restoring!.id, ...(source ? { from: source } : {}) };
      const out = await api<{ deploy: string }>("POST", path + "/restore", { ...from, into, recovery_box: box });
      setKey("");
      setRestoring(null);
      setDone({ into, deploy: out.deploy });
    } catch (e) {
      setRErr(errText(e));
    }
  });

  return (
    <div className="flex flex-col gap-3">
      <div className="flex flex-wrap items-center justify-between gap-2.5">
        <h2>Backups</h2>
        <Button variant="primary" onClick={() => run(async () => {
          try { await api("POST", path + "/backups"); list.reload(); } catch (e) { setErr(errText(e)); }
        })}>Back up now</Button>
      </div>
      <p className="text-kumo-subtle">{nightly ? `Daily, encrypted to your recovery key, kept off this server: ${kept(view.data?.form.keep)}.`
        : "Not backed up every night. Turn it on with backup: true in yawl.yaml; Back up now still works."}</p>
      {view.data?.form.kind === "clickhouse" && (
        <p className="text-kumo-subtle">A materialized view without a TO table comes back with its definition but without the rows it had collected.</p>
      )}
      {err && <Callout tone="bad">{err}</Callout>}
      {w?.pitr && (
        <div className="flex flex-wrap items-center justify-between gap-2.5">
          <p>{w.from ? `Any second from ${dateTime(w.from)} to ${dateTime(w.to)}.` : "Point in time recovery starts with the next nightly backup."}</p>
          {w.from > 0 && <Button onClick={() => (setRestoring("time"), setInto(`${service}-restored`))}>Restore to a time</Button>}
        </div>
      )}
      {w?.pitr && w.shipped > 0 && Date.now() - w.shipped > 10 * 60_000 && (
        <Callout tone="warn">{`Changes were last uploaded ${ago(w.shipped)}; a restore reaches only that far. Check the backup storage.`}</Callout>
      )}
      {done && (
        <Callout tone="ok" title={`Restoring into ${done.into}.`}>
          Once its deploy is live, point an app at it: set {REF_KEY[view.data?.form.kind ?? ""] ?? "DATABASE_URL"} to the reference {done.into}.url, then deploy.{" "}
          <Link className={linkVariants({ variant: "inline" })} to={`/p/${project}/${env}/s/${done.into}/deploys/${done.deploy}`}>Follow the restore</Link>
        </Callout>
      )}
      {!list.data ? <Loading what={list} /> : list.data.length === 0 ? <p className="text-kumo-subtle">No backups yet.</p> : (
        <BackupRows rows={list.data} onRestore={(b) => (setRestoring(b), setInto(`${service}-restored`), setSource(""))} />
      )}
      {restoring && (
        <Modal title={restoring === "time" ? "Restore to a time" : `Restore the backup of ${dateTime(restoring.created_at)}`} onClose={() => setRestoring(null)}>
          <p className="text-kumo-subtle">A restore always goes into a new database. The live one is not touched.</p>
          {restoring === "time" && <Field label="Time">{(id) => <Input id={id} className="w-full font-mono" type="datetime-local" step={1} value={at} onChange={(e) => setAt(e.target.value)} />}</Field>}
          <Field label="New database name">{(id) => <Input id={id} className="w-full" value={into} onChange={(e) => setInto(e.target.value)} />}</Field>
          {restoring !== "time" && <ReadFrom b={restoring} value={source} onChange={setSource} />}
          <Field label="Recovery key" hint="Sealed in this browser to the agent that restores. The server never sees it.">
            {(id) => <SensitiveInput id={id} className="w-full font-mono text-sm" autoComplete="off" spellCheck={false} value={key} onChange={(e) => setKey(e.target.value)} />}
          </Field>
          {rErr && <Callout tone="bad">{rErr}</Callout>}
          <div className="flex flex-wrap items-center gap-2.5">
            <Button variant="primary" onClick={restore}>Restore into a new database</Button>
            <Button variant="ghost" onClick={() => setRestoring(null)}>Cancel</Button>
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
    <div className="flex flex-col gap-3">
      <h2>Backups</h2>
      <p className="text-kumo-subtle">{`Each volume nightly, encrypted to your recovery key, kept off this server: ${kept(view.data?.form.keep)}.`}</p>
      <Callout tone="warn">A volume is read while the app runs: a file being written at that moment may be cut. Keep what must be exact in Postgres.</Callout>
      {err && <Callout tone="bad">{err}</Callout>}
      {done && (
        <Callout tone="ok" title={`Restoring ${done.volume}.`}>
          <Link className={linkVariants({ variant: "inline" })} to={`/p/${project}/${env}/s/${service}/deploys/${done.deploy}`}>Follow the restore</Link>
        </Callout>
      )}
      {!list.data ? <Loading what={list} /> : volumes.map((v) => {
        const rows = list.data!.filter((b) => b.volume === v.name);
        return (
          <section key={v.name} className="flex flex-col gap-3">
            <div className="flex flex-wrap items-center justify-between gap-2.5">
              <h3>{v.name} <span className="font-mono text-kumo-subtle">{v.path}</span></h3>
              <Button aria-label={`Back up ${v.name} now`} disabled={backing.includes(v.name)} onClick={() => backUp(v.name)}>Back up now</Button>
            </div>
            {rows.length === 0 ? <p className="text-kumo-subtle">No backups of {v.name} yet.</p> : <BackupRows rows={rows} onRestore={(b) => (setRestoring(b), setTyped(""), setSource(""))} />}
          </section>
        );
      })}
      {restoring && (
        <Modal title={`Restore ${restoring.volume}`} onClose={() => setRestoring(null)}>
          <p>{service} stops, {restoring.volume} is emptied and filled from the backup of {dateTime(restoring.created_at)}, then {service} starts.</p>
          <ReadFrom b={restoring} value={source} onChange={setSource} />
          <Field label="Recovery key" hint="Sealed in this browser to the agent that restores. The server never sees it.">
            {(id) => <SensitiveInput id={id} className="w-full font-mono text-sm" autoComplete="off" spellCheck={false} value={key} onChange={(e) => setKey(e.target.value)} />}
          </Field>
          <Field label={`Type ${service} to confirm`}>{(id) => <Input id={id} className="w-full font-mono" autoComplete="off" value={typed} onChange={(e) => setTyped(e.target.value)} />}</Field>
          {rErr && <Callout tone="bad">{rErr}</Callout>}
          <div className="flex flex-wrap items-center gap-2.5">
            <Button variant="destructive" disabled={typed !== service} onClick={restore}>Restore {restoring.volume}</Button>
            <Button variant="ghost" onClick={() => setRestoring(null)}>Cancel</Button>
          </div>
        </Modal>
      )}
    </div>
  );
}
