import { useEffect, useState } from "react";
import { api } from "../api";
import { dateTime } from "../format";
import { Shell } from "../Shell";
import { ServerTabs } from "./ServerTabs";
import { Button, cn, inputVariants, LayerCard, Select, Table, TagInput } from "@cloudflare/kumo";
import { Callout, Confirm, Field, Link, Loading, errText, useLoad, Card } from "../ui";

type FwRule = { proto: string; port: number; from?: string[] };
type Rules = { on: boolean; allow: FwRule[]; block: string[]; ssh_from: string[] };
type Status = {
  on: boolean; available: boolean; ufw: boolean; version: string;
  bans: { ip: string; until: number }[];
  ports: { proto: string; port: number; address: string; process?: string; exposure: string }[];
};
export type View = {
  rules: Rules; trial?: Rules; trial_until?: number; yawl: (FwRule & { why: string })[];
  your_address: string; status: Status | null; status_error?: string; telegram: boolean;
};

const PROTO: Record<string, string> = { tcp: "TCP", udp: "UDP", both: "Both" };
const proto = (p: string) => PROTO[p] ?? p.toUpperCase();
const EXPOSURE: Record<string, string> = { everyone: "open to everyone", some: "open to some", closed: "closed by the firewall" };
const list = (s: string) => s.split(/[\s,]+/).filter(Boolean);
export function portError(s: string): string {
  const t = s.trim();
  return /^\d+$/.test(t) && Number(t) >= 1 && Number(t) <= 65535 ? "" : "A port is a whole number from 1 to 65535.";
}
const from = (r: FwRule) => (r.from?.length ? r.from.join(", ") : "everyone");
const IN = inputVariants({ focusIndicator: true });
const MONO = cn(IN, "font-mono");

export function Firewall({ name }: { name: string }) {
  const v = useLoad<View>(`/api/servers/${name}/firewall`);
  const [draft, setDraft] = useState<Rules | null>(null);
  const [port, setPort] = useState("");
  const [newProto, setNewProto] = useState("tcp");
  const [src, setSrc] = useState("");
  const [err, setErr] = useState("");
  const [confirm, setConfirm] = useState(false);
  const [portErr, setPortErr] = useState("");
  const [turningOff, setTurningOff] = useState(false);
  const [now, setNow] = useState(Date.now());
  const base = `/api/servers/${name}/firewall`;

  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(t);
  }, []);
  useEffect(() => setDraft(v.data ? (v.data.trial ?? v.data.rules) : null), [v.data]);

  async function act(f: () => Promise<unknown>) {
    setErr("");
    setConfirm(false);
    try {
      await f();
      v.reload();
    } catch (e) {
      const text = errText(e);
      setErr(text);
      setConfirm(text.startsWith("Your address "));
    }
  }
  const save = (r: Rules, anyway = false) => act(() => api("PUT", base, anyway ? { ...r, confirm_address: true } : r));

  const d = v.data;
  const trialLeft = d?.trial_until ? Math.max(0, Math.round((d.trial_until - now) / 1000)) : 0;
  return (
    <Shell crumb={[<Link to="/servers">Servers</Link>, name, "Firewall"]} active="/servers">
      <ServerTabs name={name} active="firewall" />
      {!d || !draft ? <Loading what={v} /> : (
        <div className="flex flex-col gap-3">
          {d.status_error && <Callout tone="warn">{d.status_error}</Callout>}
          {d.status?.ufw && <Callout tone="">ufw is on too; both apply.</Callout>}
          {d.status && !d.status.available ? <Callout tone="warn">The firewall needs nftables (Debian 12, Ubuntu 22.04 or later).</Callout> : (
            <>
              {trialLeft > 0 && (
                <Callout tone="warn" title="Check that you can still reach this server by SSH. Keep these rules?">
                  <div className="flex flex-wrap items-center gap-2.5">
                    <Button variant="primary" onClick={() => act(() => api("POST", base + "/keep"))}>Keep</Button>
                    <span>{trialLeft} s left, then the rules from before come back.</span>
                  </div>
                </Callout>
              )}
              {!draft.on ? (
                <LayerCard className="flex flex-col items-start gap-3 p-4">
                  <p>Off. Every port this server listens on is open to everyone.</p>
                  <Button variant="primary" onClick={() => save({ ...draft, on: true })}>Turn on</Button>
                </LayerCard>
              ) : (
                <>
                  <LayerCard className="overflow-x-auto p-0">
                    <Table>
                      <Table.Header><Table.Row><Table.Head>Open by Yawl</Table.Head><Table.Head>Port</Table.Head><Table.Head>From</Table.Head></Table.Row></Table.Header>
                      <Table.Body>
                        {d.yawl.map((r) => (
                          <Table.Row key={r.why + r.port} className="text-kumo-subtle"><Table.Cell>{r.why}</Table.Cell><Table.Cell className="font-mono">{proto(r.proto)} {r.port}</Table.Cell><Table.Cell>{from(r)}</Table.Cell></Table.Row>
                        ))}
                      </Table.Body>
                    </Table>
                  </LayerCard>
                  <Card title="Your rules">
                    {draft.allow.length > 0 && (
                      <Table>
                        <Table.Header><Table.Row><Table.Head>Port</Table.Head><Table.Head>From</Table.Head><Table.Head /></Table.Row></Table.Header>
                        <Table.Body>
                          {draft.allow.map((r, i) => (
                            <Table.Row key={i}>
                              <Table.Cell className="font-mono">{proto(r.proto)} {r.port}</Table.Cell><Table.Cell>{from(r)}</Table.Cell>
                              <Table.Cell className="text-right"><Button size="sm" variant="ghost" className="text-kumo-danger" onClick={() => setDraft({ ...draft, allow: draft.allow.filter((_, j) => j !== i) })}>Remove</Button></Table.Cell>
                            </Table.Row>
                          ))}
                        </Table.Body>
                      </Table>
                    )}
                    <div className="flex flex-col gap-1.5">
                      <div className="flex flex-wrap items-end gap-2.5">
                        <Field label="Port">{(id) => <input id={id} className={cn(MONO, "w-[100px]")} inputMode="numeric" aria-invalid={!!portErr} value={port} onChange={(e) => { setPort(e.target.value); setPortErr(""); }} />}</Field>
                        <Field label="Protocol">{(id) => (
                          <Select aria-labelledby={id + "-label"} value={newProto} onValueChange={(v) => setNewProto(String(v))} items={PROTO} />
                        )}</Field>
                        <Field label="From">{(id) => <input id={id} className={MONO} placeholder="203.0.113.0/24" value={src} onChange={(e) => setSrc(e.target.value)} />}</Field>
                        <Button disabled={!port} onClick={() => {
                          const bad = portError(port);
                          if (bad) return setPortErr(bad);
                          setDraft({ ...draft, allow: [...draft.allow, { proto: newProto, port: Number(port.trim()), from: list(src) }] });
                          setPort(""); setSrc("");
                        }}>Add rule</Button>
                      </div>
                      {portErr && <span className="text-sm text-kumo-danger" role="alert">{portErr}</span>}
                      <span className="text-sm text-kumo-subtle">An empty From is everyone.</span>
                    </div>
                    <Field label="Block" hint="Addresses or ranges dropped before any rule, SSH too.">
                      {(id) => <TagInput id={id} className="font-mono" value={draft.block} onValueChange={(v) => setDraft({ ...draft, block: v })} />}
                    </Field>
                    <Field label="SSH from" hint={`Empty is everyone. Your address is ${d.your_address}.`}>
                      {(id) => <TagInput id={id} className="font-mono" value={draft.ssh_from} onValueChange={(v) => setDraft({ ...draft, ssh_from: v })} />}
                    </Field>
                    <div className="flex flex-wrap items-center gap-2.5">
                      <Button variant="primary" onClick={() => save(draft)}>Save</Button>
                      {confirm && <Button onClick={() => save(draft, true)}>Save anyway</Button>}
                      <Button variant="ghost" onClick={() => setTurningOff(true)}>Turn off</Button>
                    </div>
                  </Card>
                </>
              )}
              {err && <Callout tone="bad">{err}</Callout>}
              {d.status && d.status.bans.length > 0 && (
                <LayerCard className="overflow-x-auto p-0">
                  <Table>
                    <Table.Header><Table.Row><Table.Head>Banned for SSH</Table.Head><Table.Head>Until</Table.Head><Table.Head /></Table.Row></Table.Header>
                    <Table.Body>
                      {d.status.bans.map((b) => (
                        <Table.Row key={b.ip}>
                          <Table.Cell className="font-mono">{b.ip}</Table.Cell><Table.Cell>{dateTime(b.until)}</Table.Cell>
                          <Table.Cell><Button size="sm" variant="ghost" aria-label={`Unban ${b.ip}`} onClick={() => act(() => api("POST", base + "/unban", { ip: b.ip }))}>Unban</Button></Table.Cell>
                        </Table.Row>
                      ))}
                    </Table.Body>
                  </Table>
                </LayerCard>
              )}
              <div className="flex flex-wrap items-center gap-2.5">
                <span>{d.telegram ? "SSH bans go to Telegram." : "SSH bans are in the audit log."}</span>
                <Button size="sm" onClick={() => act(() => api("PUT", "/api/settings/firewall", { telegram: !d.telegram }))}>
                  {d.telegram ? "Stop sending them to Telegram" : "Send them to Telegram too"}
                </Button>
              </div>
              {d.status && (
                <LayerCard className="overflow-x-auto p-0">
                  <Table>
                    <Table.Header><Table.Row><Table.Head>Listening</Table.Head><Table.Head>Address</Table.Head><Table.Head>Process</Table.Head><Table.Head>Reachable</Table.Head></Table.Row></Table.Header>
                    <Table.Body>
                      {d.status.ports.map((p) => (
                        <Table.Row key={p.proto + p.port + p.address}>
                          <Table.Cell className="font-mono">{proto(p.proto)} {p.port}</Table.Cell><Table.Cell className="font-mono">{p.address}</Table.Cell>
                          <Table.Cell>{p.process || ""}</Table.Cell><Table.Cell>{EXPOSURE[p.exposure] ?? p.exposure}</Table.Cell>
                        </Table.Row>
                      ))}
                    </Table.Body>
                  </Table>
                </LayerCard>
              )}
            </>
          )}
        </div>
      )}
      {turningOff && draft && (
        <Confirm title="Turn off the firewall?" yes="Yes, turn it off" onClose={() => setTurningOff(false)}
          onYes={async () => { await api("PUT", base, { ...draft, on: false }); v.reload(); }}>
          <p>Every port opens to everyone.</p>
        </Confirm>
      )}
    </Shell>
  );
}
