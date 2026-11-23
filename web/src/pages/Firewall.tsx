import { useEffect, useState } from "react";
import { api } from "../api";
import { dateTime } from "../format";
import { Shell } from "../Shell";
import { ServerTabs } from "./ServerTabs";
import { Callout, Confirm, Field, Link, Loading, errText, useLoad } from "../ui";

type FwRule = { proto: string; port: number; from?: string[] };
type Rules = { on: boolean; allow: FwRule[]; block: string[]; ssh_from: string[] };
type Status = {
  on: boolean; available: boolean; ufw: boolean; version: string;
  bans: { ip: string; until: number }[];
  ports: { proto: string; port: number; address: string; process?: string; exposure: string }[];
};
type View = {
  rules: Rules; trial?: Rules; trial_until?: number; yawl: (FwRule & { why: string })[];
  your_address: string; status: Status | null; status_error?: string; telegram: boolean;
};

const EXPOSURE: Record<string, string> = { everyone: "open to everyone", some: "open to some", closed: "closed by the firewall" };
const list = (s: string) => s.split(/[\s,]+/).filter(Boolean);
export function portError(s: string): string {
  const t = s.trim();
  return /^\d+$/.test(t) && Number(t) >= 1 && Number(t) <= 65535 ? "" : "A port is a whole number from 1 to 65535.";
}
const from = (r: FwRule) => (r.from?.length ? r.from.join(", ") : "everyone");

export function Firewall({ name }: { name: string }) {
  const v = useLoad<View>(`/api/servers/${name}/firewall`);
  const [draft, setDraft] = useState<Rules | null>(null);
  const [port, setPort] = useState("");
  const [proto, setProto] = useState("tcp");
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
      <h1>{name}</h1>
      <ServerTabs name={name} active="firewall" />
      {!d || !draft ? <Loading what={v} /> : (
        <div className="col" style={{ maxWidth: 880 }}>
          {d.status_error && <Callout tone="warn">{d.status_error}</Callout>}
          {d.status?.ufw && <Callout tone="">ufw is on too; both apply.</Callout>}
          {d.status && !d.status.available ? <Callout tone="warn">The firewall needs nftables (Debian 12, Ubuntu 22.04 or later).</Callout> : (
            <>
              {trialLeft > 0 && (
                <Callout tone="warn" title="Check that you can still reach this server by SSH. Keep these rules?">
                  <div className="row">
                    <button className="btn pri" onClick={() => act(() => api("POST", base + "/keep"))}>Keep</button>
                    <span>{trialLeft} s left, then the rules from before come back.</span>
                  </div>
                </Callout>
              )}
              {!draft.on ? (
                <div className="card pad col">
                  <p>Off. Every port this server listens on is open to everyone.</p>
                  <div><button className="btn pri" onClick={() => save({ ...draft, on: true })}>Turn on</button></div>
                </div>
              ) : (
                <>
                  <div className="card scroll">
                    <table className="tbl">
                      <thead><tr><th>Open by Yawl</th><th>Port</th><th>From</th></tr></thead>
                      <tbody>
                        {d.yawl.map((r) => (
                          <tr key={r.why + r.port} className="dim"><td>{r.why}</td><td className="mono">{r.proto} {r.port}</td><td>{from(r)}</td></tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                  <div className="card pad col">
                    <h2>Your rules</h2>
                    {draft.allow.length > 0 && (
                      <table className="tbl">
                        <thead><tr><th>Port</th><th>From</th><th /></tr></thead>
                        <tbody>
                          {draft.allow.map((r, i) => (
                            <tr key={i}>
                              <td className="mono">{r.proto} {r.port}</td><td>{from(r)}</td>
                              <td><button className="btn sm ghost" onClick={() => setDraft({ ...draft, allow: draft.allow.filter((_, j) => j !== i) })}>Remove</button></td>
                            </tr>
                          ))}
                        </tbody>
                      </table>
                    )}
                    <div className="row" style={{ alignItems: "flex-end" }}>
                      <Field label="Port" hint={portErr && <span className="bad" role="alert">{portErr}</span>}>{(id) => <input id={id} className="in mono" inputMode="numeric" style={{ width: 100 }} value={port} onChange={(e) => { setPort(e.target.value); setPortErr(""); }} />}</Field>
                      <Field label="Protocol">{(id) => (
                        <select id={id} className="in" value={proto} onChange={(e) => setProto(e.target.value)}>
                          <option value="tcp">TCP</option><option value="udp">UDP</option><option value="both">Both</option>
                        </select>
                      )}</Field>
                      <Field label="From" hint="Empty is everyone.">{(id) => <input id={id} className="in mono" placeholder="203.0.113.0/24" value={src} onChange={(e) => setSrc(e.target.value)} />}</Field>
                      <button className="btn" disabled={!port} onClick={() => {
                        const bad = portError(port);
                        if (bad) return setPortErr(bad);
                        setDraft({ ...draft, allow: [...draft.allow, { proto, port: Number(port.trim()), from: list(src) }] });
                        setPort(""); setSrc("");
                      }}>Add rule</button>
                    </div>
                    <Field label="Block" hint="Addresses or ranges dropped before any rule, SSH too.">
                      {(id) => <input id={id} className="in mono" value={draft.block.join(", ")} onChange={(e) => setDraft({ ...draft, block: list(e.target.value) })} />}
                    </Field>
                    <Field label="SSH from" hint={`Empty is everyone. Your address is ${d.your_address}.`}>
                      {(id) => <input id={id} className="in mono" value={draft.ssh_from.join(", ")} onChange={(e) => setDraft({ ...draft, ssh_from: list(e.target.value) })} />}
                    </Field>
                    <div className="row">
                      <button className="btn pri" onClick={() => save(draft)}>Save</button>
                      {confirm && <button className="btn" onClick={() => save(draft, true)}>Save anyway</button>}
                      <button className="btn ghost" onClick={() => setTurningOff(true)}>Turn off</button>
                    </div>
                  </div>
                </>
              )}
              {err && <Callout tone="bad">{err}</Callout>}
              {d.status && d.status.bans.length > 0 && (
                <div className="card scroll">
                  <table className="tbl">
                    <thead><tr><th>Banned for SSH</th><th>Until</th><th /></tr></thead>
                    <tbody>
                      {d.status.bans.map((b) => (
                        <tr key={b.ip}>
                          <td className="mono">{b.ip}</td><td>{dateTime(b.until)}</td>
                          <td><button className="btn sm ghost" aria-label={`Unban ${b.ip}`} onClick={() => act(() => api("POST", base + "/unban", { ip: b.ip }))}>Unban</button></td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              )}
              <div className="row">
                <span>{d.telegram ? "SSH bans go to Telegram." : "SSH bans are in the audit log."}</span>
                <button className="btn sm" onClick={() => act(() => api("PUT", "/api/settings/firewall", { telegram: !d.telegram }))}>
                  {d.telegram ? "Stop sending them to Telegram" : "Send them to Telegram too"}
                </button>
              </div>
              {d.status && (
                <div className="card scroll">
                  <table className="tbl">
                    <thead><tr><th>Listening</th><th>Address</th><th>Process</th><th>Reachable</th></tr></thead>
                    <tbody>
                      {d.status.ports.map((p) => (
                        <tr key={p.proto + p.port + p.address}>
                          <td className="mono">{p.proto} {p.port}</td><td className="mono">{p.address}</td>
                          <td>{p.process || ""}</td><td>{EXPOSURE[p.exposure] ?? p.exposure}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
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
