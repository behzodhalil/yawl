import { useState, type FormEvent } from "react";
import { Button, LayerCard, SensitiveInput, Table } from "@cloudflare/kumo";
import { api } from "../../api";
import { Callout, Card, Confirm, Field, Loading, TestConnection, errText, useLoad, Input } from "../../ui";

type Login = { host: string; username: string };

export function Registries() {
  const list = useLoad<Login[]>("/api/registries");
  const [host, setHost] = useState("");
  const [user, setUser] = useState("");
  const [token, setToken] = useState("");
  const [err, setErr] = useState("");
  const [ask, setAsk] = useState<Login | null>(null);

  async function add(e: FormEvent) {
    e.preventDefault();
    setErr("");
    try {
      await api("PUT", `/api/registries/${encodeURIComponent(host.trim())}`, { username: user, token });
      setHost(""); setUser(""); setToken("");
      list.reload();
    } catch (e) {
      setErr(errText(e));
    }
  }

  return (
    <div className="flex max-w-[720px] flex-col gap-3">
      {!list.data ? <Loading what={list} /> : list.data.length > 0 && (
        <LayerCard className="overflow-x-auto p-0">
        <Table>
          <Table.Header><Table.Row><Table.Head>Host</Table.Head><Table.Head>User</Table.Head><Table.Head /></Table.Row></Table.Header>
          <Table.Body>
            {list.data.map((r) => (
              <Table.Row key={r.host}>
                <Table.Cell className="font-mono text-sm">{r.host}</Table.Cell><Table.Cell>{r.username}</Table.Cell>
                <Table.Cell className="text-right"><div className="flex flex-wrap items-start justify-end gap-2.5"><TestConnection label={`Test ${r.host}`} path={`/api/registries/${encodeURIComponent(r.host)}/test`} /><Button variant="ghost" className="text-kumo-danger" aria-label={`Remove ${r.host}`} onClick={() => setAsk(r)}>Remove</Button></div></Table.Cell>
              </Table.Row>
            ))}
          </Table.Body>
        </Table>
        </LayerCard>
      )}
      <Card title="Add a registry" level={3}>
        <form className="flex flex-col gap-4" onSubmit={add}>
          <div className="grid grid-cols-[repeat(auto-fit,minmax(min(320px,100%),1fr))] gap-4">
            <Field label="Host">{(id) => <Input id={id} className="font-mono text-sm" placeholder="ghcr.io" value={host} onChange={(e) => setHost(e.target.value)} />}</Field>
            <Field label="User">{(id) => <Input id={id} value={user} onChange={(e) => setUser(e.target.value)} />}</Field>
          </div>
          <Field label="Token">{(id) => <SensitiveInput id={id} className="font-mono text-sm" autoComplete="off" value={token} onChange={(e) => setToken(e.target.value)} />}</Field>
          <div className="flex flex-wrap items-center gap-3 border-t border-kumo-hairline pt-4"><Button type="submit" variant="primary" disabled={!host.trim() || !user || !token}>Add</Button></div>
        </form>
      </Card>
      {err && <Callout tone="bad">{err}</Callout>}
      {ask && <Confirm title={`Remove ${ask.host}?`} yes="Yes, remove" onClose={() => setAsk(null)} onYes={async () => { await api("DELETE", `/api/registries/${encodeURIComponent(ask.host)}`); list.reload(); }}>
        <p>Services that pull private images from it can no longer deploy.</p>
      </Confirm>}
    </div>
  );
}
