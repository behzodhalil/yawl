import { useEffect, useRef, useState } from "react";
import { api } from "../api";
import { Shell } from "../Shell";
import { ServerTabs } from "./ServerTabs";
import { Button, LayerCard } from "@cloudflare/kumo";
import { Callout, Link, Loading, POLL_MS, errText, useLoad } from "../ui";

export type Status = { Updates: number; Security: number; Reboot: boolean; RebootPkgs?: string[]; Auto: boolean; Installer: boolean; Running: boolean; Log?: string };

export const updatesText = (s: Status) => `${s.Updates} ${s.Updates === 1 ? "update" : "updates"} waiting${s.Security > 0 ? `, ${s.Security} security` : ""}`;

const REBOOT_PKGS_SHOWN = 3;

export function rebootText(pkgs: string[] = []) {
  const shown = pkgs.slice(0, REBOOT_PKGS_SHOWN).join(", ");
  const more = pkgs.length > REBOOT_PKGS_SHOWN ? ` +${pkgs.length - REBOOT_PKGS_SHOWN} more` : "";
  return `Reboot required to finish an update${pkgs.length ? ` (${shown}${more})` : ""}`;
}

export function OSUpdates({ name }: { name: string }) {
  const [started, setStarted] = useState(false);
  const [running, setRunning] = useState(false);
  const st = useLoad<Status>(`/api/servers/${encodeURIComponent(name)}/os`, running ? POLL_MS : undefined);
  const seen = useRef(false);
  const runningNow = st.data?.Running;
  useEffect(() => {
    if (runningNow) seen.current = true;
    else if (runningNow === false && seen.current) { seen.current = false; setStarted(false); }
  }, [runningNow]);
  const now = !!st.data && (st.data.Running || started);
  if (now !== running) setRunning(now);
  const [err, setErr] = useState("");
  const s = st.data;
  return (
    <Shell crumb={[<Link to="/servers">Servers</Link>, name, "OS updates"]} active="/servers">
      <ServerTabs name={name} active="os" />
      {!s ? <Loading what={st} /> : (
        <LayerCard className="flex max-w-[720px] flex-col gap-3 p-4">
          <p>{s.Updates < 0 ? "This server does not say how many updates wait (no update-notifier)."
            : s.Updates === 0 ? "Up to date." : updatesText(s)}</p>
          {s.Reboot && <Callout tone="warn" title={rebootText(s.RebootPkgs)}>Restart this server at a quiet time: sudo reboot</Callout>}
          {!s.Installer ? <p className="text-kumo-subtle">unattended-upgrades is not installed: sudo apt-get install unattended-upgrades</p> : (
            <>
              <p className="text-kumo-subtle">{s.Auto ? "Security updates install by themselves each day." : "Updates do not install by themselves on this server."}</p>
              <div>
                <Button disabled={s.Running || started} onClick={async () => {
                  setErr("");
                  try { await api("POST", `/api/servers/${encodeURIComponent(name)}/os/upgrade`); setStarted(true); st.reload(); } catch (e) { setErr(errText(e)); }
                }}>Install updates now</Button>
              </div>
              {(s.Running || started) && <p>Installing. It takes a few minutes; the log below shows when it is done.</p>}
            </>
          )}
          {err && <Callout tone="bad">{err}</Callout>}
          {s.Log && <div className="max-h-[420px] overflow-auto rounded-md border border-kumo-hairline bg-kumo-recessed py-2 font-mono text-xs leading-relaxed [&>div]:px-3.5 [&>div]:break-words [&>div]:whitespace-pre-wrap">{s.Log.split("\n").map((l, i) => <div key={i}>{l}</div>)}</div>}
        </LayerCard>
      )}
    </Shell>
  );
}
