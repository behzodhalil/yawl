import { useState, type FormEvent } from "react";
import { api } from "../api";
import { ago } from "../format";
import { navigate } from "../router";
import { Shell } from "../Shell";
import type { ServerView } from "../types";
import { Callout, Field, Link, Loading, errText, useBusy, useLoad } from "../ui";

const connected = (s: ServerView) => !s.offline && !!s.last_heartbeat;

export const firstOnlineName = (servers: ServerView[]) => servers.find(connected)?.name ?? "";

const serverOption = (s: ServerView) =>
  s.offline ? `${s.name} (${s.public_ip}) · offline, last seen ${ago(s.last_heartbeat)}`
    : !s.last_heartbeat ? `${s.name} (${s.public_ip}) · never connected`
    : `${s.name} (${s.public_ip})`;

export function ServerSelect({ servers, value, onChange }: { servers: ServerView[]; value: string; onChange: (v: string) => void }) {
  return (
    <Field label="Server" hint="Every service of this environment runs there.">
      {(id) => (
        <select id={id} className="in" value={value} onChange={(e) => onChange(e.target.value)}>
          {servers.map((s) => (
            <option key={s.id} value={s.name} disabled={!connected(s)}>
              {serverOption(s)}
            </option>
          ))}
        </select>
      )}
    </Field>
  );
}

export function NewProject() {
  const servers = useLoad<ServerView[]>("/api/servers");
  const [name, setName] = useState("");
  const [picked, setPicked] = useState("");
  const server = picked || (servers.data ? firstOnlineName(servers.data) : "");
  const [err, setErr] = useState("");
  const [busy, run] = useBusy();

  async function submit(e: FormEvent) {
    e.preventDefault();
    setErr("");
    await run(async () => {
      try {
        await api("POST", "/api/projects", { name, server });
        navigate(`/p/${name}/production`);
      } catch (e) {
        setErr(errText(e));
      }
    });
  }

  return (
    <Shell crumb={[<Link to="/">Projects</Link>, "New project"]} active="/">
      <h1>New project</h1>
      {!servers.data ? <Loading what={servers} /> : servers.data.length === 0 ? (
        <Callout tone="warn" title="Join a server first.">
          <Link to="/servers/new">Add a server</Link>
        </Callout>
      ) : (
        <form className="card pad col w-form" onSubmit={submit}>
          <Field label="Name" hint="Lowercase letters, digits and dashes, like acme.">
            {(id) => <input id={id} className="in" value={name} onChange={(e) => setName(e.target.value)} pattern="[a-z][a-z0-9-]{0,30}" required />}
          </Field>
          {!server ? <Callout tone="warn">No server is online.</Callout> : (
            <>
              <ServerSelect servers={servers.data} value={server} onChange={setPicked} />
              {err && <Callout tone="bad">{err}</Callout>}
              <div><button className="btn pri" disabled={busy}>Create project</button></div>
            </>
          )}
        </form>
      )}
    </Shell>
  );
}
