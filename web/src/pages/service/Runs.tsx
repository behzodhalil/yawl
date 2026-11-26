import { useState } from "react";
import { api } from "../../api";
import type { CronRun, CronRuns } from "../../types";
import { Button, LayerCard, Table } from "@cloudflare/kumo";
import { took } from "../../format";
import { LogLines } from "../../LogView";
import { Callout, Icon, Loading, StatusBadge, errText, useBusy, useLiveLoad, type BadgeKind } from "../../ui";

function at(ms: number, tz: string): string {
  const p = Object.fromEntries(
    new Intl.DateTimeFormat("en-GB", { timeZone: tz, day: "numeric", month: "short", hour: "2-digit", minute: "2-digit", hourCycle: "h23" })
      .formatToParts(ms).map((x) => [x.type, x.value]),
  );
  return `${p.day} ${p.month} ${p.hour}:${p.minute}`;
}

function result(r: CronRun): [string, BadgeKind, string] {
  if (r.Skipped) return ["Skipped", "off", r.Cause];
  if (!r.FinishedAt) return ["Running", "busy", ""];
  if (r.TimedOut) return ["Failed", "bad", "Stopped at its time limit"];
  if (r.Cause) return ["Failed", "bad", r.Cause.replace(/\.+$/, "")];
  return r.Exit === 0 ? ["Succeeded", "ok", ""] : ["Failed", "bad", `Exit ${r.Exit}`];
}

export function Runs({ project, env, service }: { project: string; env: string; service: string }) {
  const path = `/api/services/${project}/${env}/${service}/runs`;
  const runs = useLiveLoad<CronRuns>(path, (d) => d.running || d.runs.some((r) => !r.Skipped && !r.FinishedAt));
  const [, run] = useBusy();
  const [err, setErr] = useState("");
  const [note, setNote] = useState("");
  const [open, setOpen] = useState("");
  if (!runs.data) return <Loading what={runs} />;
  const d = runs.data;
  return (
    <div className="flex flex-col gap-3">
      <div className="flex flex-wrap items-center gap-3">
        <span className="text-kumo-subtle">{d.next ? `Next run: ${at(d.next, d.timezone)} (${d.timezone})` : "No run is scheduled within a year."}</span>
        <Button size="sm" onClick={() => run(async () => {
          setErr(""); setNote("");
          try { await api("POST", path); setNote("Started."); runs.reload(); } catch (e) { setErr(errText(e)); }
        })}>Run now</Button>
      </div>
      {note && <p className="text-kumo-subtle">{note}</p>}
      {err && <Callout tone="bad">{err}</Callout>}
      {d.runs.length === 0 ? <p className="text-kumo-subtle">No runs yet.</p> : (
        <LayerCard className="overflow-x-auto p-0">
          <Table>
            <Table.Header><Table.Row><Table.Head>Started</Table.Head><Table.Head>Took</Table.Head><Table.Head>Result</Table.Head></Table.Row></Table.Header>
            <Table.Body>
              {d.runs.flatMap((r) => {
                const [word, kind, why] = result(r);
                const started = at(r.Skipped ? r.ScheduledAt : r.StartedAt, d.timezone);
                return [
                  <Table.Row key={r.ID}>
                    <Table.Cell className="font-mono text-sm tabular-nums">
                      {r.Output ? (
                        <button type="button" aria-expanded={open === r.ID} aria-label={`Last lines of the run at ${started}`} className="flex items-center gap-2 text-left"
                          onClick={() => setOpen(open === r.ID ? "" : r.ID)}>
                          <Icon name={open === r.ID ? "down" : "chev"} />{started}
                        </button>
                      ) : started}
                    </Table.Cell>
                    <Table.Cell className="font-mono text-sm tabular-nums text-kumo-subtle">{r.FinishedAt && !r.Skipped ? took(r.FinishedAt - r.StartedAt) : ""}</Table.Cell>
                    <Table.Cell>
                      <div className="flex flex-wrap items-center gap-2"><StatusBadge kind={kind}>{word}</StatusBadge>{why && <span className="text-sm text-kumo-subtle">{why}</span>}</div>
                    </Table.Cell>
                  </Table.Row>,
                  ...(open === r.ID ? [
                    <Table.Row key={r.ID + "-lines"}>
                      <Table.Cell colSpan={3}><div className="logs"><LogLines rows={r.Output.split("\n").map((text) => ({ text }))} /></div></Table.Cell>
                    </Table.Row>,
                  ] : []),
                ];
              })}
            </Table.Body>
          </Table>
        </LayerCard>
      )}
    </div>
  );
}
