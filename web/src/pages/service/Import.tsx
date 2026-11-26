import { useState } from "react";
import { api, ApiError } from "../../api";
import { Button, linkVariants } from "@cloudflare/kumo";
import { Callout, Confirm, Field, Link, errText, Input, Card } from "../../ui";

const SERVER_HINT = "import-server-hint";

export function ImportDump({ project, env, service }: { project: string; env: string; service: string }) {
  const path = `/api/services/${project}/${env}/${service}/import`;
  const [file, setFile] = useState<File | null>(null);
  const [link, setLink] = useState("");
  const [name, setName] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const [deploy, setDeploy] = useState("");
  const [pct, setPct] = useState<number | null>(null);
  const [ask, setAsk] = useState<(() => Promise<{ deploy: string }>) | null>(null);
  const [typed, setTyped] = useState("");

  async function start(run: () => Promise<{ deploy: string }>) {
    setErr(""), setBusy(true), setPct(null);
    try { setDeploy((await run()).deploy); } catch (e) { setErr(errText(e)); } finally { setBusy(false); }
  }
  // XHR, not fetch: only it reports upload progress.
  function upload(f: File): Promise<{ deploy: string }> {
    return new Promise((resolve, reject) => {
      const x = new XMLHttpRequest();
      x.open("PUT", `${path}?name=${encodeURIComponent(f.name)}`);
      x.withCredentials = true;
      x.setRequestHeader("X-Yawl-Request", "1");
      x.upload.onprogress = (e) => e.lengthComputable && setPct(Math.floor((e.loaded / e.total) * 100));
      x.onerror = () => reject(new Error("The upload failed; check the connection."));
      x.onload = () => {
        if (x.status < 200 || x.status >= 300) return reject(new ApiError(x.status, x.responseText.trim() || x.statusText));
        try { resolve(JSON.parse(x.responseText)); } catch (e) { reject(e); }
      };
      x.send(f);
    });
  }
  const confirm = (run: () => Promise<{ deploy: string }>) => (setTyped(""), setAsk(() => run));

  return (
    <Card title="Import a dump" level={3} className="max-w-[var(--w-form)]">
      <p className="text-kumo-subtle">A pg_dump (custom or SQL), a mysqldump or a mongodump --archive, gzipped or not. A snapshot of this database is kept on its server first. What the dump drops or replaces is changed here.</p>
      <div className="flex flex-wrap items-center gap-2.5">
        <input type="file" aria-label="Dump file" onChange={(e) => setFile(e.target.files?.[0] ?? null)} />
        <Button disabled={!file || busy} onClick={() => file && confirm(() => upload(file))}>Upload and import</Button>
      </div>
      <div className="flex flex-wrap items-end gap-2.5">
        <div className="min-w-0 flex-1"><Field label="Link">{(id) => <Input id={id} className="w-full" value={link} placeholder="https://… (a presigned S3 link works)" onChange={(e) => setLink(e.target.value)} />}</Field></div>
        <Button disabled={!link || busy} onClick={() => confirm(() => api("POST", path, { url: link.trim() }))}>Import from the link</Button>
      </div>
      <div className="flex flex-col gap-1.5">
        <div className="flex flex-wrap items-end gap-2.5">
          <div className="min-w-0 flex-1"><Field label="File on the server">{(id) => <Input id={id} className="w-full" aria-describedby={SERVER_HINT} value={name} onChange={(e) => setName(e.target.value)} />}</Field></div>
          <Button disabled={!name || busy} onClick={() => confirm(() => api("POST", path, { path: name.trim() }))}>Import from the server</Button>
        </div>
        <span id={SERVER_HINT} className="text-sm text-kumo-subtle">Its name in /var/lib/yawl/agent/imports</span>
      </div>
      {busy && <p className="text-kumo-subtle">{pct === null ? "Starting the import…" : `Uploading ${pct}%`}</p>}
      {ask && (
        <Confirm title={`Import into ${service}?`} yes="Yes, import" disabled={typed !== service} onClose={() => setAsk(null)} onYes={async () => { start(ask); }}>
          <p>A snapshot of {service} is kept first. What the dump drops or replaces is changed here.</p>
          <Field label={`Type ${service} to confirm`}>{(id) => <Input id={id} className="w-full font-mono" autoComplete="off" value={typed} onChange={(e) => setTyped(e.target.value)} />}</Field>
        </Confirm>
      )}
      {err && <Callout tone="bad">{err}</Callout>}
      {deploy && <Callout tone="ok">The import started. <Link className={linkVariants({ variant: "inline" })} to={`/p/${project}/${env}/s/${service}/deploys/${deploy}`}>Follow the import</Link></Callout>}
    </Card>
  );
}
