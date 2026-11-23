import { useEffect, useRef, useState } from "react";
import { api } from "../api";
import { Shell } from "../Shell";
import { ServerTabs } from "./ServerTabs";
import { Callout, Link, Loading, POLL_MS, errText, useLoad } from "../ui";

export type Status = { Updates: number; Security: number; Reboot: boolean; RebootPkgs?: string[]; Auto: boolean; Installer: boolean; Running: boolean; Log?: string };

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
      <h1>{name}</h1>
      <ServerTabs name={name} active="os" />
      {!s ? <Loading what={st} /> : (
        <div className="card pad col w-form">
          <p>{s.Updates < 0 ? "This server does not say how many updates wait (no update-notifier)."
            : s.Updates === 0 ? "Up to date." : `${s.Updates} updates wait, ${s.Security} of them security updates.`}</p>
          {s.Reboot && <Callout tone="warn">{`Restart this server at a quiet time to finish an update${s.RebootPkgs?.length ? ` (${s.RebootPkgs.join(", ")})` : ""}: sudo reboot`}</Callout>}
          {!s.Installer ? <p className="muted">unattended-upgrades is not installed: sudo apt-get install unattended-upgrades</p> : (
            <>
              <p className="muted">{s.Auto ? "Security updates install by themselves each day." : "Updates do not install by themselves on this server."}</p>
              <div>
                <button className="btn" disabled={s.Running || started} onClick={async () => {
                  setErr("");
                  try { await api("POST", `/api/servers/${encodeURIComponent(name)}/os/upgrade`); setStarted(true); st.reload(); } catch (e) { setErr(errText(e)); }
                }}>Install updates now</button>
              </div>
              {(s.Running || started) && <p>Installing. It takes a few minutes; the log below shows when it is done.</p>}
            </>
          )}
          {err && <Callout tone="bad">{err}</Callout>}
          {s.Log && <div className="logs">{s.Log.split("\n").map((l, i) => <div key={i}>{l}</div>)}</div>}
        </div>
      )}
    </Shell>
  );
}
