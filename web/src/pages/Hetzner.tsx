import { useState } from "react";
import { api } from "../api";
import { Button, Combobox, inputVariants, Loader, Select, SensitiveInput } from "@cloudflare/kumo";
import { Callout, Confirm, Field, Icon, Loading, errText, useBusy, useLoad, Card } from "../ui";

const IN = inputVariants({ focusIndicator: true });

type Size = { value: string; label: string };

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
  const sizes: Size[] = (c.data.types ?? []).filter(sold).map((t) => ({ value: t.name, label: `${t.name}: ${t.cores} cores, ${t.memory} GB, ${t.disk} GB disk${t.prices[location] ? `, ${t.prices[location]} € a month` : ""}` }));
  return (
    <Card title={<><Icon name="hetzner" />Create on Hetzner</>} className="max-w-[var(--w-form)]">
      {!c.data.connected ? (
        <>
          <Field label="Hetzner API token" hint="Cloud Console, your project, Security, API tokens, Read & Write.">
            {(id) => <SensitiveInput id={id} className={IN} value={token} onChange={(e) => setToken(e.target.value)} />}
          </Field>
          <Button className="self-start" disabled={!token || busy} onClick={() => once(() => run(async () => { await api("PUT", "/api/settings/hetzner", { token }); setToken(""); c.reload(); }))}>Connect Hetzner</Button>
        </>
      ) : c.data.error ? (
        <Callout tone="bad">{c.data.error}</Callout>
      ) : (
        <>
          <Field label="Name">{(id) => <input id={id} className={IN} value={name} onChange={(e) => setName(e.target.value)} placeholder="fsn2" />}</Field>
          <Field label="Location">
            {(id) => <Select aria-labelledby={id + "-label"} value={location} items={Object.fromEntries(c.data!.locations!.map((l) => [l.name, `${l.name}: ${l.city}`]))}
              onValueChange={(v) => { setLoc(String(v)); setType(""); }} />}
          </Field>
          <Combobox label="Size" items={sizes} value={sizes.find((x) => x.value === kind) ?? null}
            onValueChange={(v) => setType((v as Size | null)?.value ?? "")} isItemEqualToValue={(x, y) => (x as Size).value === (y as Size).value}>
            <Combobox.TriggerValue className="w-full" placeholder="Pick a size" />
            <Combobox.Content>
              <Combobox.Input placeholder="Search sizes" />
              <Combobox.Empty>No size matches.</Combobox.Empty>
              <Combobox.List>{(x: Size) => <Combobox.Item key={x.value} value={x}>{x.label}</Combobox.Item>}</Combobox.List>
            </Combobox.Content>
          </Combobox>
          <div className="flex flex-wrap items-center gap-3 border-t border-kumo-hairline pt-4">
            <Button icon={busy ? <span aria-hidden="true" className="inline-flex"><Loader size={14} /></span> : undefined} variant="primary" disabled={!name || busy} onClick={() => once(() => run(async () => {
              const s = await api<{ ip: string }>("POST", "/api/hetzner/servers", { name, type: kind, location });
              setMade(`${name} is being made at ${s.ip}. It installs Yawl and joins in a few minutes, then appears in Servers.`);
            }))}>{busy ? "Creating…" : "Create the server"}</Button>
            <Button onClick={() => setLeaving(true)}>Disconnect Hetzner</Button>
          </div>
        </>
      )}
      {made && <Callout tone="ok">{made}</Callout>}
      {c.data.made?.map((m) => m.late
        ? <Callout key={m.name} tone="warn">{`${m.name} (${m.ip}) did not join in 15 minutes. Read /var/log/cloud-init-output.log on it, or delete it in the Hetzner console.`}</Callout>
        : <p key={m.name} className="text-kumo-subtle">{`${m.name} (${m.ip}) is installing Yawl.`}</p>)}
      {err && <Callout tone="bad">{err}</Callout>}
      {leaving && (
        <Confirm title="Disconnect Hetzner?" yes="Yes, disconnect" onClose={() => setLeaving(false)}
          onYes={async () => { await api("DELETE", "/api/settings/hetzner"); c.reload(); }}>
          <p>Yawl forgets the API token. Servers already made stay on Hetzner.</p>
        </Confirm>
      )}
    </Card>
  );
}
