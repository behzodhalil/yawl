import { useState } from "react";
import { Button, LayerCard, Select, SensitiveInput, Switch, Table } from "@cloudflare/kumo";
import { api } from "../../api";
import { setShared, type SharedScope } from "../../seal";
import { Ago, Callout, Card, Field, Input, Loading, Modal, errText, useLoad } from "../../ui";

const TEAM = "*";

type Entry = { scope: "team" | "server"; key: string; servers: string[]; missing?: string[]; stale?: string[]; build?: boolean; updated_at: number };

export function SharedVars() {
  const list = useLoad<Entry[]>("/api/shared-env");
  const servers = useLoad<{ servers: { name: string }[] }>("/api/shared-env/keys");
  const [key, setKey] = useState("");
  const [value, setValue] = useState("");
  const [where, setWhere] = useState<SharedScope>({ kind: "team" });
  const [build, setBuild] = useState(false);
  const [err, setErr] = useState("");
  const [saved, setSaved] = useState(false);
  const [removing, setRemoving] = useState<Entry | null>(null);
  async function set() {
    setErr(""); setSaved(false);
    try {
      await setShared(where, key.trim(), value, build);
      setKey(""), setValue(""), setSaved(true), list.reload();
    } catch (e) {
      setErr(errText(e));
    }
  }
  async function remove(e: Entry) {
    const path = e.scope === "team" ? `/api/shared-env/team/${encodeURIComponent(e.key)}` : `/api/shared-env/servers/${encodeURIComponent(e.servers[0])}/${encodeURIComponent(e.key)}`;
    setRemoving(null);
    try { await api("DELETE", path); list.reload(); } catch (x) { setErr(errText(x)); }
  }
  return (
    <div className="flex flex-col gap-3">
      <p className="text-kumo-subtle">Team variables reach every app, server variables every app on that server; an app's own variable wins. Changes apply at the next deploy.</p>
      {err && <Callout tone="bad">{err}</Callout>}
      {!list.data ? <Loading what={list} /> : (
        <LayerCard className="overflow-x-auto p-0">
          <Table>
            <Table.Header><Table.Row><Table.Head>Name</Table.Head><Table.Head>Where</Table.Head><Table.Head>Changed</Table.Head><Table.Head /></Table.Row></Table.Header>
            <Table.Body>
              {list.data.length === 0 ? <Table.Row><Table.Cell colSpan={4} className="text-kumo-subtle">None yet.</Table.Cell></Table.Row> : list.data.map((e) => (
                <Table.Row key={e.scope + e.key + e.servers.join()}>
                  <Table.Cell className="font-mono text-sm">{e.key}{e.build && <span className="text-kumo-subtle"> (build too)</span>}</Table.Cell>
                  <Table.Cell>
                    {e.scope === "team" ? "Team" : `Server ${e.servers[0]}`}
                    {e.missing && e.missing.length > 0 && <div className="text-kumo-subtle">{`Not on ${e.missing.join(", ")}: set it again to reach it.`}</div>}
                    {e.stale && e.stale.length > 0 && <div className="text-kumo-subtle">{`${e.stale.join(", ")} joined again since: set it again, or its deploys stop.`}</div>}
                  </Table.Cell>
                  <Table.Cell className="text-kumo-subtle"><Ago ms={e.updated_at} /></Table.Cell>
                  <Table.Cell><Button size="sm" variant="ghost" className="text-kumo-danger" onClick={() => setRemoving(e)}>Delete</Button></Table.Cell>
                </Table.Row>
              ))}
            </Table.Body>
          </Table>
        </LayerCard>
      )}
      {removing && (
        <Modal title={`Delete ${removing.key}?`} onClose={() => setRemoving(null)}>
          <p>{removing.scope === "team" ? "Every app" : `Every app on ${removing.servers[0]}`} goes without it from its next deploy, unless it sets its own.</p>
          <div className="flex flex-wrap items-center gap-2.5">
            <Button variant="destructive" onClick={() => remove(removing)}>{`Delete ${removing.key}`}</Button>
            <Button variant="ghost" onClick={() => setRemoving(null)}>Cancel</Button>
          </div>
        </Modal>
      )}
      <Card title="Set a variable" level={3} className="max-w-[var(--w-form)]">
        <div className="grid gap-4 sm:grid-cols-2">
          <Field label="Name">{(id) => <Input id={id} className="font-mono" value={key} onChange={(e) => { setKey(e.target.value); setSaved(false); }} placeholder="SENTRY_DSN" />}</Field>
          <Field label="Value">{(id) => <SensitiveInput id={id} className="font-mono" autoComplete="off" value={value} onChange={(e) => { setValue(e.target.value); setSaved(false); }} />}</Field>
          <Field label="Where">{(id) => (
            <Select aria-labelledby={id + "-label"} value={where.kind === "server" ? where.name : TEAM} onValueChange={(v) => { setWhere(v === TEAM ? { kind: "team" } : { kind: "server", name: String(v) }); setSaved(false); }}
              items={[{ value: TEAM, label: "Team: every server" }, ...(servers.data?.servers ?? []).map((s) => ({ value: s.name, label: `Server ${s.name}` }))]} />
          )}</Field>
        </div>
        <Switch label="Also give it to builds" checked={build} onClick={() => { setBuild(!build); setSaved(false); }} />
        <div className="flex flex-wrap items-center gap-3 border-t border-kumo-hairline pt-4">
          <Button variant="primary" disabled={!key || !value} onClick={set}>Set</Button>
          {saved ? <span className="text-kumo-subtle">Saved</span> : (!key || !value) && <span className="text-kumo-subtle">Name and Value are needed</span>}
        </div>
      </Card>
    </div>
  );
}
