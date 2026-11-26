import { useEffect, useState } from "react";
import { api } from "../api";
import { sealTo } from "../seal";
import { Shell } from "../Shell";
import { ServerTabs } from "./ServerTabs";
import { Button, cn, inputVariants, LayerCard, Loader, SensitiveInput } from "@cloudflare/kumo";
import { Callout, Confirm, Field, Link, Loading, POLL_MS, errText, useBusy, useLoad } from "../ui";

export const JOIN_LIMIT = 3 * 60_000;

export function Tailscale({ name }: { name: string }) {
  const path = `/api/servers/${encodeURIComponent(name)}/tailscale`;
  const [joined, setJoined] = useState(false);
  const [leaving, setLeaving] = useState(false);
  const [busy, run] = useBusy();
  const [key, setKey] = useState("");
  const [err, setErr] = useState("");
  const [poll, setPoll] = useState(false);
  const [slow, setSlow] = useState(false);
  const [round, setRound] = useState(0);
  const st = useLoad<{ running: boolean; outside?: boolean; address?: string; agent_pubkey: string }>(path, poll ? POLL_MS : undefined);
  const joining = !!st.data && ((st.data.running && !st.data.address) || (joined && !st.data.running));
  useEffect(() => {
    setSlow(false);
    if (!joining) return;
    const t = setTimeout(() => setSlow(true), JOIN_LIMIT);
    return () => clearTimeout(t);
  }, [joining, round]);
  const polling = joining && !slow;
  if (polling !== poll) setPoll(polling);
  const join = () => run(async () => {
    setErr("");
    try {
      if (!st.data?.agent_pubkey) throw new Error(`Server ${name} has no secrets key; join it again.`);
      const auth_key_box = await sealTo(st.data.agent_pubkey, new TextEncoder().encode(key.trim()));
      await api("PUT", path, { auth_key_box });
      setKey("");
      setJoined(true);
      st.reload();
    } catch (e) {
      setErr(errText(e));
    }
  });
  return (
    <Shell crumb={[<Link to="/servers">Servers</Link>, name, "Tailscale"]} active="/servers">
      <ServerTabs name={name} active="tailscale" />
      <p className="text-kumo-subtle">Puts this server on your tailnet, so the panel and SSH can be reached from your devices. The tailnet's access rules decide who.</p>
      {err && <Callout tone="bad">{err}</Callout>}
      <LayerCard className="flex max-w-[720px] flex-col gap-3 p-4">
        {!st.data ? <Loading what={st} /> : st.data.running ? (
          <>
            <p>{st.data.address ? `On the tailnet at ${st.data.address}.` : "Joining the tailnet."}</p>
            <Button variant="ghost" className="self-start" onClick={() => setLeaving(true)}>Leave the tailnet</Button>
          </>
        ) : st.data.outside ? <p>{`Tailscale is installed outside Yawl here, at ${st.data.address}. Yawl leaves it as it is.`}</p>
          : <p className="text-kumo-subtle">Not on a tailnet.</p>}
        {joining && slow && (
          <div className="flex flex-col items-start gap-3">
            <p className="text-kumo-subtle">Joining is taking longer than expected.</p>
            <Button onClick={() => { setSlow(false); setRound((n) => n + 1); }}>Check again</Button>
          </div>
        )}
        {st.data && !st.data.running && !st.data.outside && (
          <>
            <Field label="Auth key" hint="From the tailnet's admin console: a reusable, pre-approved key. Sealed in this browser to the server.">
              {(id) => <SensitiveInput id={id} className={cn(inputVariants({ focusIndicator: true }), "font-mono")} autoComplete="off" value={key} onChange={(e) => setKey(e.target.value)} />}
            </Field>
            <Button icon={busy ? <span aria-hidden="true" className="inline-flex"><Loader size={14} /></span> : undefined} variant="primary" className="self-start" disabled={!key.trim() || busy} onClick={join}>{busy ? "Joining…" : "Join the tailnet"}</Button>
          </>
        )}
      </LayerCard>
      {leaving && (
        <Confirm title="Leave the tailnet?" yes="Yes, leave" onClose={() => setLeaving(false)}
          onYes={async () => { await api("DELETE", path); setJoined(false); st.reload(); }}>
          <p>The panel and SSH may stop being reachable over Tailscale.</p>
        </Confirm>
      )}
    </Shell>
  );
}
