import { useState } from "react";
import { api, ApiError } from "../../api";
import { Callout, Confirm, Field, Link, errText } from "../../ui";

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
    <div className="card pad col">
      <h3>Import a dump</h3>
      <p className="muted">A pg_dump (custom or SQL), a mysqldump or a mongodump --archive, gzipped or not. A snapshot of this database is kept on its server first. What the dump drops or replaces is changed here.</p>
      <div className="row">
        <input type="file" aria-label="Dump file" onChange={(e) => setFile(e.target.files?.[0] ?? null)} />
        <button className="btn" disabled={!file || busy} onClick={() => file && confirm(() => upload(file))}>Upload and import</button>
      </div>
      <div className="row">
        <Field label="Link">{(id) => <input id={id} className="in" value={link} placeholder="https://… (a presigned S3 link works)" onChange={(e) => setLink(e.target.value)} />}</Field>
        <button className="btn" disabled={!link || busy} onClick={() => confirm(() => api("POST", path, { url: link.trim() }))}>Import from the link</button>
      </div>
      <div className="row">
        <Field label="File on the server" hint="Its name in /var/lib/yawl/agent/imports">{(id) => <input id={id} className="in" value={name} onChange={(e) => setName(e.target.value)} />}</Field>
        <button className="btn" disabled={!name || busy} onClick={() => confirm(() => api("POST", path, { path: name.trim() }))}>Import from the server</button>
      </div>
      {busy && <p className="muted">{pct === null ? "Starting the import…" : `Uploading ${pct}%`}</p>}
      {ask && (
        <Confirm title={`Import into ${service}?`} yes="Yes, import" disabled={typed !== service} onClose={() => setAsk(null)} onYes={async () => { start(ask); }}>
          <p>A snapshot of {service} is kept first. What the dump drops or replaces is changed here.</p>
          <Field label={`Type ${service} to confirm`}>{(id) => <input id={id} className="in mono" autoComplete="off" value={typed} onChange={(e) => setTyped(e.target.value)} />}</Field>
        </Confirm>
      )}
      {err && <Callout tone="bad">{err}</Callout>}
      {deploy && <Link to={`/p/${project}/${env}/s/${service}/deploys/${deploy}`}>Follow the import</Link>}
    </div>
  );
}
