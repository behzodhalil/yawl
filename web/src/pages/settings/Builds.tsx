import { useState } from "react";
import { Button, Select } from "@cloudflare/kumo";
import { api } from "../../api";
import { Callout, Field, Loading, errText, useLoad, Input } from "../../ui";

export function Builds() {
  const cur = useLoad<{ server: string; repository: string }>("/api/settings/builds");
  const servers = useLoad<{ name: string }[]>("/api/servers");
  if (!cur.data || !servers.data) return <Loading what={cur.data ? servers : cur} />;
  return <BuildsForm cur={cur.data} servers={servers.data} />;
}

function BuildsForm({ cur, servers }: { cur: { server: string; repository: string }; servers: { name: string }[] }) {
  const [server, setServer] = useState(cur.server);
  const [repo, setRepo] = useState(cur.repository);
  const [msg, setMsg] = useState("");
  const [err, setErr] = useState("");
  async function save() {
    setErr(""); setMsg("");
    try {
      await api("PUT", "/api/settings/builds", { server, repository: server ? repo.trim() : "" });
      setMsg(server ? `Git builds run on ${server} and are pushed to ${repo.trim()}.` : "Each server builds its own.");
    } catch (e) {
      setErr(errText(e));
    }
  }
  return (
    <div className="flex max-w-[720px] flex-col gap-3">
      <Field label="Build on">
        {(id) => <Select aria-labelledby={id + "-label"} value={server} onValueChange={(v) => setServer(String(v))}
          items={[{ value: "", label: "Each server builds its own" }, ...servers.map((s) => ({ value: s.name, label: s.name }))]} />}
      </Field>
      {server && (
        <Field label="Push to" hint="A repository whose registry has a login in Registries, like ghcr.io/acme/builds. Each build is tagged with its deploy.">
          {(id) => <Input id={id} value={repo} onChange={(e) => setRepo(e.target.value)} placeholder="ghcr.io/acme/builds" />}
        </Field>
      )}
      <p className="text-kumo-subtle">A service with build variables still builds on its own server: they are sealed to it. The build server must have the same CPU kind as the others.</p>
      <div><Button variant="primary" onClick={save}>Save</Button></div>
      {msg && <Callout tone="ok">{msg}</Callout>}
      {err && <Callout tone="bad">{err}</Callout>}
    </div>
  );
}
