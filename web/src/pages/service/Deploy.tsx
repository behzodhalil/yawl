import { createContext, useContext, useEffect, useState } from "react";
import { api } from "../../api";
import { ago, dateTime, deployWord, shortRef, shortSha, stepName, took } from "../../format";
import { LogLines } from "../../LogView";
import type { DeployView, StepView } from "../../types";
import { Callout, StatusBadge, errText } from "../../ui";

const TERMINAL = new Set(["live", "failed", "superseded", "rolled_back"]);
const LOUD = /error|fatal|panic|exception/i;

export const DeployEnded = createContext<() => void>(() => {});

export function Deploy({ id }: { project: string; env: string; service: string; id: string }) {
  const [d, setD] = useState<DeployView | null>(null);
  const [open, setOpen] = useState(false);
  const [cancelErr, setCancelErr] = useState("");
  const [lost, setLost] = useState(false);
  const ended = useContext(DeployEnded);
  useEffect(() => {
    let es: EventSource | undefined, timer: ReturnType<typeof setTimeout> | undefined, over = false;
    const open = () => {
      es = new EventSource(`/api/deploys/${id}/stream`);
      es.onmessage = (e) => {
        const v = JSON.parse(e.data) as DeployView;
        setD(v);
        setLost(false);
        if (TERMINAL.has(v.state)) { over = true; es?.close(); ended(); }
      };
      es.onerror = () => {
        es?.close();
        if (over) return;
        setLost(true);
        timer = setTimeout(open, 3000);
      };
    };
    open();
    return () => { over = true; clearTimeout(timer); es?.close(); };
  }, [id, ended]);
  const note = lost && <Callout tone="warn">Lost the live connection. Reconnecting…</Callout>;
  if (!d) return <>{note}<p className="muted">Waiting for the deploy…</p></>;
  const [word, tone] = deployWord(d.state, d.live, d.auto_rolled_back, d.cause);
  const cancelable = ["queued", "building", "pulling"].includes(d.state);
  const failed = d.steps.find((s) => s.state === "failed");
  const why = failed || (TERMINAL.has(d.state) && d.cause);
  return (
    <div className="col">
      {note}
      <div className="card">
        <div className="pad" style={{ borderBottom: "1px solid var(--line)" }}>
          <div className="row" style={{ justifyContent: "space-between" }}>
            <h2>{d.subject || shortSha(d.commit) || d.id}</h2>
            <span className="row" style={{ gap: 8 }}>
              {cancelable && <button className="btn sm ghost" onClick={() => { setCancelErr(""); api("POST", `/api/deploys/${id}/cancel`).catch((e) => setCancelErr(errText(e))); }}>Cancel</button>}
              <StatusBadge kind={tone}>{word}</StatusBadge>
            </span>
          </div>
          {cancelErr && <Callout tone="bad">{cancelErr}</Callout>}
          <div className="dsub" title={dateTime(d.created_at)}>{[d.subject && shortRef(d), d.by, ago(d.created_at), d.restart ? "restart" : d.rollback && "rollback"].filter(Boolean).join(" · ")}</div>
        </div>
        <table className="tbl">
          <tbody>
            {d.steps.map((s) => (
              <tr key={s.step} className={s.state === "failed" ? "word bad" : ""}>
                <td>{stepName(s.step)}{s.state === "failed" && " failed"}</td>
                <td className={"num" + (s.state === "failed" ? "" : " muted")} data-u="right">{s.finished_at ? took(s.finished_at - s.started_at) || "0s" : "running"}</td>
              </tr>
            ))}
          </tbody>
        </table>
        {why ? <Log lines={failedLines(d.cause, failed)} red={(d.cause ?? "").split("\n")[0]} />
          : d.steps.some((s) => s.output) && (open ? <Log lines={d.steps.flatMap((s) => (s.output ? [`==> ${stepName(s.step)}`, ...s.output.split("\n")] : []))} />
            : <div className="pad" style={{ borderTop: "1px solid var(--line)" }}><button className="linkbtn" onClick={() => setOpen(true)}>View log</button></div>)}
      </div>
      {d.warnings.map((w) => <Callout key={w} tone="warn">{w}</Callout>)}
    </div>
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
    <div className="logs" data-testid="log" style={{ border: 0, borderTop: "1px solid var(--line)" }}>
      <LogLines rows={lines.map((l) => ({ text: l, bad: l === red || LOUD.test(l) }))} />
    </div>
  );
}
