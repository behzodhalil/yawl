import { useEffect, useRef, useState } from "react";
import { Terminal as XTerm } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import "@xterm/xterm/css/xterm.css";
import { Button, cn, InlineCopyText } from "@cloudflare/kumo";
import { Ago, Callout, Card, Skeleton, useLoad } from "../../ui";
import { api } from "../../api";
import type { ServiceView } from "../../types";

type Props = { project: string; env: string; service: string };
type Opened = { at: number; actor: string; kind: string; container?: string };

const SESSIONS_SHOWN = 10;

export function Terminal(p: Props) {
  const [started, setStarted] = useState(false);
  return (
    <div className="flex flex-col gap-6">
      {started ? <Session {...p} onEnd={() => setStarted(false)} /> : (
        <Card title={`Open a shell in ${p.service} (${p.env})`} level={3}>
          <p className="text-kumo-subtle">This is a root shell in the running container. The session is written to the audit log.</p>
          <Button variant="primary" onClick={() => setStarted(true)}>Open terminal</Button>
        </Card>
      )}
      <Sessions {...p} />
    </div>
  );
}

function Sessions({ project, env, service }: Props) {
  const view = useLoad<ServiceView>(`/api/services/${project}/${env}/${service}`);
  const id = view.data?.id;
  const list = useLoad<{ rows: Opened[] }>(id ? `/api/audit?from=0&q=${encodeURIComponent(`"service":"${id}"`)}` : null);
  const rows = list.data?.rows.filter((r) => r.kind === "terminal.opened").sort((a, b) => b.at - a.at).slice(0, SESSIONS_SHOWN);
  const err = view.error ?? list.error;
  return (
    <Card title="Recent sessions" level={3}>
      <p className="text-kumo-subtle">Who opened a shell here and when. What was typed is not recorded.</p>
      {err ? <p className="text-kumo-subtle">{err}</p> : !rows ? <Skeleton /> : rows.length === 0 ? <p className="text-kumo-subtle">No sessions yet.</p> : (
        <ul aria-label="Recent sessions" className="m-0 flex list-none flex-col divide-y divide-kumo-hairline p-0">
          {rows.map((r) => (
            <li key={`${r.at}-${r.actor}`} className="flex flex-wrap items-center gap-x-3 gap-y-1 py-2">
              <span className="font-medium">{r.actor}</span>
              <span className="text-kumo-subtle"><Ago ms={r.at} /></span>
              {r.container && <span className="ml-auto"><InlineCopyText>{r.container}</InlineCopyText></span>}
            </li>
          ))}
        </ul>
      )}
    </Card>
  );
}

function Session({ project, env, service, onEnd }: Props & { onEnd: () => void }) {
  const box = useRef<HTMLDivElement>(null);
  const [ended, setEnded] = useState("");
  const [open, setOpen] = useState(false);
  const [attempt, setAttempt] = useState(0);
  useEffect(() => {
    const term = new XTerm({ fontSize: 13, cursorBlink: true, fontFamily: "ui-monospace, SFMono-Regular, Menlo, monospace" });
    const fit = new FitAddon();
    term.loadAddon(fit);
    term.open(box.current!);
    fit.fit();
    const scheme = location.protocol === "https:" ? "wss" : "ws";
    const path = `/api/services/${project}/${env}/${service}/terminal`;
    const ws = new WebSocket(`${scheme}://${location.host}${path}?cols=${term.cols}&rows=${term.rows}`);
    ws.binaryType = "arraybuffer";
    let opened = false;
    ws.onopen = () => { opened = true; setOpen(true); };
    ws.onmessage = (e) => {
      if (typeof e.data === "string") {
        const m = JSON.parse(e.data) as { end?: string };
        if (m.end) setEnded(m.end);
        return;
      }
      term.write(new Uint8Array(e.data as ArrayBuffer));
    };
    ws.onclose = () => {
      if (opened) { setEnded((s) => s || "the connection closed"); return; }
      // A refused upgrade is unreadable to a WebSocket: the same URL asked plainly gives the panel's words.
      api("GET", path).then(() => setEnded("the connection closed"), (e: Error) => setEnded(e.message));
    };
    const enc = new TextEncoder();
    term.onData((d) => { if (ws.readyState === 1) ws.send(enc.encode(d)); });
    const onResize = () => {
      fit.fit();
      if (ws.readyState === 1) ws.send(JSON.stringify({ cols: term.cols, rows: term.rows }));
    };
    window.addEventListener("resize", onResize);
    return () => { window.removeEventListener("resize", onResize); ws.onclose = null; ws.close(); term.dispose(); };
  }, [project, env, service, attempt]);
  return (
    <div className="flex flex-col gap-3">
      <div className={cn("term-strip flex items-center justify-between gap-3 rounded-lg border border-kumo-line px-2.5 py-1.5 font-mono text-xs font-medium", env === "production" && "prod border-kumo-danger text-kumo-danger")}>
        <span>{env}</span>
        <Button size="sm" onClick={onEnd}>End session</Button>
      </div>
      {!open && !ended && <p className="text-kumo-subtle">Connecting…</p>}
      {ended && <Callout tone="warn">{`Session ended: ${ended}${/[.!?]$/.test(ended) ? "" : "."}`}</Callout>}
      {ended && <div><Button onClick={() => (setEnded(""), setOpen(false), setAttempt((a) => a + 1))}>Reconnect</Button></div>}
      <div ref={box} className="h-[60vh] overflow-hidden rounded-lg border border-kumo-line bg-black p-2" />
    </div>
  );
}
