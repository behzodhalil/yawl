import { useEffect, useState, type FormEvent } from "react";
import { api } from "../../api";
import { Callout, Confirm, Field, Loading, TestConnection, errText, useBusy, useLoad } from "../../ui";

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
    <form className="card pad col w-form" onSubmit={add}>
      <h2>Copies</h2>
      <p className="muted">Each backup is also copied here, still encrypted, so a restore works when the main bucket is gone.</p>
      {copies.length > 0 && (
        <div className="card scroll">
          <table className="tbl">
            <thead><tr><th>Name</th><th>Endpoint</th><th>Bucket</th><th /></tr></thead>
            <tbody>
              {copies.map((c) => (
                <tr key={c.name}>
                  <td>{c.name}</td><td className="mono">{c.endpoint}</td><td className="mono">{c.bucket}</td>
                  <td><button type="button" className="btn sm ghost" aria-label={`Remove ${c.name}`} onClick={() => setAsk(c.name)}>Remove</button></td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {copies.length < 3 && (
        <>
          <div className="grid2">
            <Field label="Copy name" hint="Like eu or backblaze">{(id) => <input id={id} className="in mono" value={f.name} onChange={up("name")} />}</Field>
            <Field label="Copy endpoint">{(id) => <input id={id} className="in mono" value={f.endpoint} onChange={up("endpoint")} />}</Field>
            <Field label="Copy bucket">{(id) => <input id={id} className="in mono" value={f.bucket} onChange={up("bucket")} />}</Field>
            <Field label="Copy region">{(id) => <input id={id} className="in mono" value={f.region} onChange={up("region")} />}</Field>
            <Field label="Copy access key">{(id) => <input id={id} className="in mono" autoComplete="off" value={f.access_key} onChange={up("access_key")} />}</Field>
            <Field label="Copy secret key">{(id) => <input id={id} className="in mono" type="password" autoComplete="off" value={f.secret_key} onChange={up("secret_key")} />}</Field>
          </div>
          <div><button className="btn" disabled={!f.name || !f.endpoint || !f.bucket || !f.access_key || !f.secret_key}>Add copy</button></div>
        </>
      )}
      {err && <Callout tone="bad">{err}</Callout>}
      {ask && <Confirm title={`Remove the copy "${ask}"?`} yes="Yes, remove" onClose={() => setAsk("")} onYes={async () => { await api("DELETE", `/api/settings/storage/copies/${encodeURIComponent(ask)}`); reload(); }}>
        <p>New backups stop going there. What is already stored there stays.</p>
      </Confirm>}
    </form>
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
        setMsg(["ok", "Saved. The next backup goes there."]);
        setSaves((n) => n + 1);
        cur.reload();
      } catch (e) {
        setMsg(["bad", errText(e)]);
      }
    });
  }

  return (
    <div className="col">
    <form className="card pad col w-form" onSubmit={save}>
      <h2>Backup storage</h2>
      <p className="muted">Any S3-compatible bucket: AWS S3, Backblaze B2, Cloudflare R2, Alibaba OSS. Backups are encrypted to your recovery key before they leave the server.</p>
      <Field label="Endpoint" hint="Host only, like s3.eu-central-003.backblazeb2.com">{(id) => <input id={id} className="in mono" value={f.endpoint} onChange={up("endpoint")} />}</Field>
      <div className="grid2">
        <Field label="Bucket">{(id) => <input id={id} className="in mono" value={f.bucket} onChange={up("bucket")} />}</Field>
        <Field label="Region" hint="Leave empty if the provider has none.">{(id) => <input id={id} className="in mono" value={f.region} onChange={up("region")} />}</Field>
      </div>
      <Field label="Access key" hint={cur.data.access_key_end ? `The access key ends in ${cur.data.access_key_end}.` : undefined}>{(id) => <input id={id} className="in mono" autoComplete="off" value={f.access_key} onChange={up("access_key")} />}</Field>
      <Field label="Secret key" hint={cur.data.set ? "Leave both empty to keep the saved keys." : undefined}>{(id) => <input id={id} className="in mono" type="password" autoComplete="off" value={f.secret_key} onChange={up("secret_key")} />}</Field>
      {msg && <Callout tone={msg[0] as "ok" | "warn" | "bad"}>{msg[1]}</Callout>}
      <div className="row" style={{ alignItems: "flex-start" }}>
        <button className="btn pri" disabled={busy}>Save storage</button>
        {cur.data.set && <TestConnection key={saves} path="/api/settings/storage/test" hint={dirty ? "Tests the saved settings." : undefined} />}
      </div>
    </form>
    <Copies copies={cur.data.copies ?? []} reload={cur.reload} />
    </div>
  );
}
