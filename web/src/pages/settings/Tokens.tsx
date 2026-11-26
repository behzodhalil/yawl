import { useState, type FormEvent } from "react";
import { Button, ClipboardText, Table } from "@cloudflare/kumo";
import { api } from "../../api";
import { Ago, Callout, Confirm, Field, Loading, Modal, errText, useBusy, useLoad, Input, Card } from "../../ui";

type Tok = { id: string; name: string; last_used_at?: number; created_at: number; member?: string };

export function Tokens() {
  const list = useLoad<Tok[]>("/api/tokens");
  const [name, setName] = useState("");
  const [made, setMade] = useState("");
  const [err, setErr] = useState("");
  const [ask, setAsk] = useState<Tok | null>(null);
  const [busy, run] = useBusy();

  async function create(e: FormEvent) {
    e.preventDefault();
    setErr("");
    await run(async () => {
      try {
        setMade((await api<{ token: string }>("POST", "/api/tokens", { name })).token);
        setName("");
      } catch (e) {
        setErr(errText(e));
      }
    });
  }

  return (
    <Card title="API tokens" className="max-w-[var(--w-form)]">
      <p className="text-kumo-subtle">For the CLI and scripts: yawl login stores one. A token can do anything the dashboard can, except change the password.</p>
      {!list.data ? <Loading what={list} /> : (
        <Table>
          <Table.Header><Table.Row><Table.Head>Name</Table.Head><Table.Head>Last used</Table.Head><Table.Head>Created</Table.Head><Table.Head /></Table.Row></Table.Header>
          <Table.Body>
            {list.data.map((t) => (
              <Table.Row key={t.id}>
                <Table.Cell>{t.name}{t.member && <div className="text-kumo-subtle">{t.member}</div>}</Table.Cell><Table.Cell><Ago ms={t.last_used_at} /></Table.Cell><Table.Cell><Ago ms={t.created_at} /></Table.Cell>
                <Table.Cell><Button size="sm" variant="ghost" className="text-kumo-danger" aria-label={`Delete ${t.name}`} onClick={() => setAsk(t)}>Delete</Button></Table.Cell>
              </Table.Row>
            ))}
          </Table.Body>
        </Table>
      )}
      <form className="flex flex-wrap items-end gap-3 border-t border-kumo-hairline pt-4" onSubmit={create}>
        <div className="flex-1"><Field label="Name">{(id) => <Input id={id} placeholder="laptop" value={name} onChange={(e) => setName(e.target.value)} required />}</Field></div>
        <Button type="submit" variant="primary" loading={busy}>Create token</Button>
      </form>
      {err && <Callout tone="bad">{err}</Callout>}
      {ask && <Confirm title={`Delete the token "${ask.name}"?`} yes="Yes, delete" onClose={() => setAsk(null)} onYes={async () => { await api("DELETE", `/api/tokens/${ask.id}`); list.reload(); }}>
        <p>Anything using it, such as the CLI or a script, stops working.</p>
      </Confirm>}
      {made && (
        <Modal title="Your new token" onClose={() => (setMade(""), list.reload())} dismissible={false}>
          <p>Copy it now. It is not shown again.</p>
          <ClipboardText className="max-w-full" text={made} />
          <div><Button variant="primary" onClick={() => (setMade(""), list.reload())}>Done</Button></div>
        </Modal>
      )}
    </Card>
  );
}
