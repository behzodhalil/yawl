import { createContext, useContext, useEffect, useState } from "react";
import { Button, Flow, LayerCard, Loader, cn } from "@cloudflare/kumo";
import { api } from "../../api";
import { actorName, ago, commitSubject, dateTime, deployWord, shortRef, shortSha, stepName, took } from "../../format";
import { LogLines } from "../../LogView";
import type { DeployRow, DeployView, StepView } from "../../types";
import { Callout, Icon, StatusBadge, errText, type Load } from "../../ui";

const TERMINAL = new Set(["live", "failed", "superseded", "rolled_back"]);
const LOUD = /error|fatal|panic|exception/i;

export const Deploys = createContext<Load<DeployRow[]>>({ reload: () => {} });

export const firstLine = (subject: string) => commitSubject(subject).split("\n")[0];

export function Author({ by }: { by?: string }) {
  if (!by) return null;
  return <span className="grid size-6 shrink-0 place-items-center rounded-sm bg-kumo-recessed text-xs font-semibold uppercase ring ring-kumo-line" aria-hidden="true">{actorName(by).charAt(0)}</span>;
}

export function Deploy({ id }: { project: string; env: string; service: string; id: string }) {
  const [d, setD] = useState<DeployView | null>(null);
  const [open, setOpen] = useState(false);
  const [cancelErr, setCancelErr] = useState("");
  const [lost, setLost] = useState(false);
  const { reload } = useContext(Deploys);
  useEffect(() => {
    let es: EventSource | undefined, timer: ReturnType<typeof setTimeout> | undefined, over = false;
    const open = () => {
      es = new EventSource(`/api/deploys/${id}/stream`);
      es.onmessage = (e) => {
        const v = JSON.parse(e.data) as DeployView;
        setD(v);
        setLost(false);
        if (TERMINAL.has(v.state)) { over = true; es?.close(); reload(); }
      };
      es.onerror = () => {
        es?.close();
        if (over) return;
        setLost(true);
        timer = setTimeout(open, 3000);
      };
    };
    reload();
    open();
    return () => { over = true; clearTimeout(timer); es?.close(); };
  }, [id, reload]);
  const note = lost && <Callout tone="warn">Lost the live connection. Reconnecting…</Callout>;
  if (!d) return <>{note}<p className="text-kumo-subtle">Waiting for the deploy…</p></>;
  const [word, tone] = deployWord(d.state, d.live, d.auto_rolled_back, d.cause);
  const cancelable = ["queued", "building", "pulling"].includes(d.state);
  const failed = d.steps.find((s) => s.state === "failed");
  const why = failed || (TERMINAL.has(d.state) && d.cause);
  return (
    <div className="flex flex-col gap-3">
      {note}
      <LayerCard>
        <div className="border-b border-kumo-hairline p-4">
          <div className="flex flex-wrap items-center justify-between gap-2.5">
            <span className="flex min-w-0 items-center gap-3"><Author by={d.by} /><h2 className="truncate">{(d.subject && firstLine(d.subject)) || shortSha(d.commit) || d.id}</h2></span>
            <span className="flex items-center gap-2">
              {cancelable && <Button variant="ghost" size="sm" onClick={() => { setCancelErr(""); api("POST", `/api/deploys/${id}/cancel`).catch((e) => setCancelErr(errText(e))); }}>Cancel</Button>}
              <StatusBadge kind={tone}>{word}</StatusBadge>
            </span>
          </div>
          {cancelErr && <Callout tone="bad">{cancelErr}</Callout>}
          <div className="mt-0.5 text-sm text-kumo-subtle" title={dateTime(d.created_at)}>{[d.subject && shortRef(d), d.by, ago(d.created_at), took(d.took_ms), d.restart ? "restart" : d.rollback && "rollback"].filter(Boolean).join(" · ")}</div>
        </div>
        <div className="overflow-x-auto p-4" aria-label="Steps">
          <Flow>
            {d.steps.map((s) => <Flow.Node key={s.step} render={<li className={cn("flex min-w-32 flex-col gap-0.5 rounded-sm bg-kumo-base px-3 py-2 ring ring-kumo-line", s.state === "failed" && "text-kumo-danger ring-kumo-danger")} />}><Step s={s} /></Flow.Node>)}
          </Flow>
        </div>
        {why ? <Log lines={failedLines(d.cause, failed)} red={(d.cause ?? "").split("\n")[0]} />
          : d.steps.some((s) => s.output) && (open ? <Log lines={d.steps.flatMap((s) => (s.output ? [`==> ${stepName(s.step)}`, ...s.output.split("\n")] : []))} />
            : <div className="border-t border-kumo-hairline p-4"><Button variant="ghost" size="sm" onClick={() => setOpen(true)}>View log</Button></div>)}
      </LayerCard>
      {d.warnings.map((w) => <Callout key={w} tone="warn">{w}</Callout>)}
    </div>
  );
}

function Step({ s }: { s: StepView }) {
  const running = !s.finished_at;
  return (
    <>
      <span className="flex items-center gap-1.5 font-medium">
        {running ? <Loader size="sm" /> : <Icon name={s.state === "failed" ? "status-bad" : "status-ok"} />}
        <span>{stepName(s.step)}{s.state === "failed" && " failed"}</span>
      </span>
      <span className={cn("font-mono text-sm tabular-nums", s.state !== "failed" && "text-kumo-subtle")}>{running ? "running" : took(s.finished_at! - s.started_at) || "0s"}</span>
    </>
  );
}

function failedLines(cause = "", s?: StepView): string[] {
  const head = cause.split("\n")[0];
  const out = (s?.output ?? "").split("\n").filter((l) => l !== "").slice(-40);
  return head && !out.some((l) => l.includes(head)) ? [head, ...out] : out;
}

function Log({ lines, red }: { lines: string[]; red?: string }) {
  if (lines.length === 0) return null;
  return (
    <div className="logs rounded-none border-0 border-t border-kumo-hairline" data-testid="log">
      <LogLines rows={lines.map((l) => ({ text: l, bad: l === red || LOUD.test(l) }))} />
    </div>
  );
}
