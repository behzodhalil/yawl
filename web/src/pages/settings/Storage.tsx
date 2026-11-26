import { useEffect, useState, type FormEvent } from "react";
import { Button, LayerCard, SensitiveInput, Table } from "@cloudflare/kumo";
import { api } from "../../api";
import { Callout, Confirm, Field, Loading, TestConnection, errText, useBusy, useLoad, Input, Card, notify } from "../../ui";

type Copy = { name: string; endpoint: string; bucket: string; region?: string; access_key_end: string };
type St = { set: boolean; endpoint?: string; bucket?: string; region?: string; access_key_end?: string; copies?: Copy[] };

function Copies({ copies, reload }: { copies: Copy[]; reload: () => void }) {
  const empty = { name: "", endpoint: "", bucket: "", region: "", access_key: "", secret_key: "" };
  const [f, setF] = useState(empty);
  const [err, setErr] = useState("");
  const [ask, setAsk] = useState("");
  const up = (k: keyof typeof f) => (e: React.ChangeEvent<HTMLInputElement>) => setF({ ...f, [k]: e.target.value });
  async function add(e: FormEvent) {
    e.preventDefault();
    setErr("");
    try {
      const { name, ...body } = f;
      await api("PUT", `/api/settings/storage/copies/${encodeURIComponent(name)}`, body);
      setF(empty), reload();
    } catch (x) {
      setErr(errText(x));
    }
  }
  return (
    <Card title="Copies" className="max-w-[var(--w-form)]">
      <form className="flex flex-col gap-4" onSubmit={add}>
        <p className="text-kumo-subtle">Each backup is also copied here, still encrypted, so a restore works when the main bucket is gone.</p>
        {copies.length > 0 && (
          <LayerCard className="overflow-x-auto p-0">
            <Table>
              <Table.Header><Table.Row><Table.Head>Name</Table.Head><Table.Head>Endpoint</Table.Head><Table.Head>Bucket</Table.Head><Table.Head /></Table.Row></Table.Header>
              <Table.Body>
                {copies.map((c) => (
                  <Table.Row key={c.name}>
                    <Table.Cell>{c.name}</Table.Cell><Table.Cell className="font-mono text-sm">{c.endpoint}</Table.Cell><Table.Cell className="font-mono text-sm">{c.bucket}</Table.Cell>
                    <Table.Cell><Button size="sm" variant="ghost" className="text-kumo-danger" aria-label={`Remove ${c.name}`} onClick={() => setAsk(c.name)}>Remove</Button></Table.Cell>
                  </Table.Row>
                ))}
              </Table.Body>
            </Table>
          </LayerCard>
        )}
        {copies.length < 3 && (
          <>
            <div className="grid gap-4 sm:grid-cols-2">
              <Field label="Copy name" hint="Like eu or backblaze">{(id) => <Input id={id} className="font-mono text-sm" value={f.name} onChange={up("name")} />}</Field>
              <Field label="Copy endpoint">{(id) => <Input id={id} className="font-mono text-sm" value={f.endpoint} onChange={up("endpoint")} />}</Field>
              <Field label="Copy bucket">{(id) => <Input id={id} className="font-mono text-sm" value={f.bucket} onChange={up("bucket")} />}</Field>
              <Field label="Copy region">{(id) => <Input id={id} className="font-mono text-sm" value={f.region} onChange={up("region")} />}</Field>
              <Field label="Copy access key">{(id) => <Input id={id} className="font-mono text-sm" autoComplete="off" value={f.access_key} onChange={up("access_key")} />}</Field>
              <Field label="Copy secret key">{(id) => <SensitiveInput id={id} className="font-mono text-sm" autoComplete="off" value={f.secret_key} onChange={up("secret_key")} />}</Field>
            </div>
            <div className="flex flex-wrap items-center gap-3 border-t border-kumo-hairline pt-4"><Button type="submit" disabled={!f.name || !f.endpoint || !f.bucket || !f.access_key || !f.secret_key}>Add copy</Button></div>
          </>
        )}
        {err && <Callout tone="bad">{err}</Callout>}
        {ask && <Confirm title={`Remove the copy "${ask}"?`} yes="Yes, remove" onClose={() => setAsk("")} onYes={async () => { await api("DELETE", `/api/settings/storage/copies/${encodeURIComponent(ask)}`); reload(); }}>
          <p>New backups stop going there. What is already stored there stays.</p>
        </Confirm>}
      </form>
    </Card>
  );
}

export function Storage() {
  const cur = useLoad<St>("/api/settings/storage");
  const [f, setF] = useState({ endpoint: "", bucket: "", region: "", access_key: "", secret_key: "" });
  const [msg, setMsg] = useState<[string, string] | null>(null);
  const [saves, setSaves] = useState(0);
  const [busy, run] = useBusy();
  useEffect(() => {
    if (cur.data?.set) setF((x) => ({ ...x, endpoint: cur.data!.endpoint ?? "", bucket: cur.data!.bucket ?? "", region: cur.data!.region ?? "" }));
  }, [cur.data]);
  if (!cur.data) return <Loading what={cur} />;
  const up = (k: keyof typeof f) => (e: React.ChangeEvent<HTMLInputElement>) => setF({ ...f, [k]: e.target.value });
  const dirty = f.endpoint !== (cur.data.endpoint ?? "") || f.bucket !== (cur.data.bucket ?? "") || f.region !== (cur.data.region ?? "") || !!f.access_key || !!f.secret_key;

  async function save(e: FormEvent) {
    e.preventDefault();
    await run(async () => {
      try {
        await api("PUT", "/api/settings/storage", f);
        setF({ ...f, access_key: "", secret_key: "" });
        setMsg(null);
        notify("Saved. The next backup goes there.");
        setSaves((n) => n + 1);
        cur.reload();
      } catch (e) {
        setMsg(["bad", errText(e)]);
      }
    });
  }

  return (
    <div className="flex flex-col gap-3">
      <Card title="Backup storage" className="max-w-[var(--w-form)]">
      <form className="flex flex-col gap-4" onSubmit={save}>
        <p className="text-kumo-subtle">Any S3-compatible bucket: AWS S3, Backblaze B2, Cloudflare R2, Alibaba OSS. Backups are encrypted to your recovery key before they leave the server.</p>
        <Field label="Endpoint" hint="Endpoint URL or host, like s3.eu-central-003.backblazeb2.com">{(id) => <Input id={id} className="font-mono text-sm" value={f.endpoint} onChange={up("endpoint")} />}</Field>
        <div className="grid gap-4 sm:grid-cols-2">
          <Field label="Bucket">{(id) => <Input id={id} className="font-mono text-sm" value={f.bucket} onChange={up("bucket")} />}</Field>
          <Field label="Region" hint="Leave empty if the provider has none.">{(id) => <Input id={id} className="font-mono text-sm" value={f.region} onChange={up("region")} />}</Field>
          <Field label="Access key" hint={cur.data.access_key_end ? `The access key ends in ${cur.data.access_key_end}.` : undefined}>{(id) => <Input id={id} className="font-mono text-sm" autoComplete="off" value={f.access_key} onChange={up("access_key")} />}</Field>
          <Field label="Secret key" hint={cur.data.set ? "Leave both empty to keep the saved keys." : undefined}>{(id) => <SensitiveInput id={id} className="font-mono text-sm" autoComplete="off" value={f.secret_key} onChange={up("secret_key")} />}</Field>
        </div>
        {msg && <Callout tone={msg[0] as "ok" | "warn" | "bad"}>{msg[1]}</Callout>}
        <div className="flex flex-wrap items-start gap-3 border-t border-kumo-hairline pt-4">
          <Button type="submit" variant="primary" loading={busy}>Save storage</Button>
          {cur.data.set && <TestConnection key={saves} path="/api/settings/storage/test" hint={dirty ? "Tests the saved settings." : undefined} />}
        </div>
      </form>
    </Card>
    <Copies copies={cur.data.copies ?? []} reload={cur.reload} />
    </div>
  );
}
