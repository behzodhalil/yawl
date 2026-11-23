import { useState } from "react";
import { api } from "../api";
import { Callout, Confirm, Field, Icon, Loading, errText, useBusy, useLoad } from "../ui";

type Choices = {
  connected: boolean;
  error?: string;
  locations?: { name: string; city: string }[];
  types?: { name: string; cores: number; memory: number; disk: number; prices: Record<string, string> }[];
  made?: { name: string; ip: string; at: number; late: boolean }[];
};

export function Hetzner() {
  const c = useLoad<Choices>("/api/hetzner");
  const [token, setToken] = useState("");
  const [name, setName] = useState("");
  const [loc, setLoc] = useState("");
  const [type, setType] = useState("");
  const [made, setMade] = useState("");
  const [err, setErr] = useState("");
  const [leaving, setLeaving] = useState(false);
  const [busy, once] = useBusy();
  const run = async (f: () => Promise<void>) => {
    setErr("");
    try { await f(); } catch (e) { setErr(errText(e)); }
  };
  if (!c.data) return <Loading what={c} />;
  const location = loc || c.data.locations?.[0]?.name || "";
  const sold = (t: { prices: Record<string, string> }) => !!t.prices[location] || !Object.keys(t.prices).length;
  const kind = type || c.data.types?.find(sold)?.name || "";
  return (
    <div className="card pad col w-form">
      <h2 className="row" style={{ gap: 8 }}><Icon name="hetzner" />Create on Hetzner</h2>
      {!c.data.connected ? (
        <>
          <Field label="Hetzner API token" hint="Cloud Console, your project, Security, API tokens, Read & Write.">
            {(id) => <input id={id} type="password" value={token} onChange={(e) => setToken(e.target.value)} />}
          </Field>
          <div><button className="btn" disabled={!token || busy} onClick={() => once(() => run(async () => { await api("PUT", "/api/settings/hetzner", { token }); setToken(""); c.reload(); }))}>Connect Hetzner</button></div>
        </>
      ) : c.data.error ? (
        <Callout tone="bad">{c.data.error}</Callout>
      ) : (
        <>
          <Field label="Name">{(id) => <input id={id} value={name} onChange={(e) => setName(e.target.value)} placeholder="fsn2" />}</Field>
          <Field label="Location">
            {(id) => <select id={id} value={location} onChange={(e) => { setLoc(e.target.value); setType(""); }}>
              {c.data!.locations!.map((l) => <option key={l.name} value={l.name}>{`${l.name}: ${l.city}`}</option>)}
            </select>}
          </Field>
          <Field label="Size">
            {(id) => <select id={id} value={kind} onChange={(e) => setType(e.target.value)}>
              {c.data!.types!.filter(sold).map((t) => (
                <option key={t.name} value={t.name}>
                  {`${t.name}: ${t.cores} cores, ${t.memory} GB, ${t.disk} GB disk${t.prices[location] ? `, ${t.prices[location]} € a month` : ""}`}
                </option>
              ))}
            </select>}
          </Field>
          <div className="row">
            <button className="btn pri" disabled={!name || busy} onClick={() => once(() => run(async () => {
              const s = await api<{ ip: string }>("POST", "/api/hetzner/servers", { name, type: kind, location });
              setMade(`${name} is being made at ${s.ip}. It installs Yawl and joins in a few minutes, then appears in Servers.`);
            }))}>{busy ? "Creating…" : "Create the server"}</button>
            <button className="btn" onClick={() => setLeaving(true)}>Disconnect Hetzner</button>
          </div>
        </>
      )}
      {made && <Callout tone="ok">{made}</Callout>}
      {c.data.made?.map((m) => m.late
        ? <Callout key={m.name} tone="warn">{`${m.name} (${m.ip}) did not join in 15 minutes. Read /var/log/cloud-init-output.log on it, or delete it in the Hetzner console.`}</Callout>
        : <p key={m.name} className="muted">{`${m.name} (${m.ip}) is installing Yawl.`}</p>)}
      {err && <Callout tone="bad">{err}</Callout>}
      {leaving && (
        <Confirm title="Disconnect Hetzner?" yes="Yes, disconnect" onClose={() => setLeaving(false)}
          onYes={async () => { await api("DELETE", "/api/settings/hetzner"); c.reload(); }}>
          <p>Yawl forgets the API token. Servers already made stay on Hetzner.</p>
        </Confirm>
      )}
    </div>
  );
}
