import { useState } from "react";
import { api } from "../../api";
import type { CronRun, CronRuns } from "../../types";
import { Callout, Loading, errText, useBusy, useLiveLoad } from "../../ui";

function at(ms: number, tz: string): string {
  const p = Object.fromEntries(
    new Intl.DateTimeFormat("en-GB", { timeZone: tz, day: "numeric", month: "short", hour: "2-digit", minute: "2-digit", hourCycle: "h23" })
      .formatToParts(ms).map((x) => [x.type, x.value]),
  );
  return `${p.day} ${p.month} ${p.hour}:${p.minute}`;
}

const took = (ms: number) => (ms < 60_000 ? `${Math.floor(ms / 1000)} s` : `${Math.floor(ms / 60_000)} min`);

function line(r: CronRun, tz: string): string {
  if (r.Skipped) return `${at(r.ScheduledAt, tz)}, ${r.Cause}`;
  if (!r.FinishedAt) return `${at(r.StartedAt, tz)}, running`;
  const end = r.TimedOut ? "stopped at its time limit" : r.Cause ? r.Cause.replace(/\.+$/, "") : `exit ${r.Exit}`;
  return `${at(r.StartedAt, tz)}, ${took(r.FinishedAt - r.StartedAt)}, ${end}`;
}

export function Runs({ project, env, service }: { project: string; env: string; service: string }) {
  const path = `/api/services/${project}/${env}/${service}/runs`;
  const runs = useLiveLoad<CronRuns>(path, (d) => d.running || d.runs.some((r) => !r.Skipped && !r.FinishedAt));
  const [, run] = useBusy();
  const [err, setErr] = useState("");
  const [note, setNote] = useState("");
  if (!runs.data) return <Loading what={runs} />;
  const d = runs.data;
  return (
    <div className="col">
      <div className="row" style={{ gap: 12 }}>
        <span className="muted">{d.next ? `Next run: ${at(d.next, d.timezone)} (${d.timezone})` : "No run is scheduled within a year."}</span>
        <button className="btn sm" onClick={() => run(async () => {
          setErr(""); setNote("");
          try { await api("POST", path); setNote("Started."); runs.reload(); } catch (e) { setErr(errText(e)); }
        })}>Run now</button>
      </div>
      {note && <p className="muted">{note}</p>}
      {err && <Callout tone="bad">{err}</Callout>}
      {d.runs.length === 0 ? <p className="muted">No runs yet.</p> : d.runs.map((r) => (
        <div key={r.ID} className="card pad col" style={{ gap: 4 }}>
          <span className={!r.Skipped && r.FinishedAt && (r.Exit !== 0 || r.TimedOut || r.Cause) ? "" : "muted"}
            style={!r.Skipped && r.FinishedAt && (r.Exit !== 0 || r.TimedOut || r.Cause) ? { color: "var(--bad)" } : undefined}>{line(r, d.timezone)}</span>
          {r.Output && (
            <details>
              <summary className="muted">Last lines</summary>
              <pre className="mono" style={{ whiteSpace: "pre-wrap", margin: 0 }}>{r.Output}</pre>
            </details>
          )}
        </div>
      ))}
    </div>
  );
}
