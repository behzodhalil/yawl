import { useEffect, useRef, useState } from "react";
import { Terminal as XTerm } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import "@xterm/xterm/css/xterm.css";
import { Callout } from "../../ui";
import { api } from "../../api";

type Props = { project: string; env: string; service: string };

export function Terminal(p: Props) {
  const [started, setStarted] = useState(false);
  if (started) return <Session {...p} onEnd={() => setStarted(false)} />;
  return (
    <div className="card col term-gate">
      <h3>{`Open a shell in ${p.service} (${p.env})`}</h3>
      <p className="muted">This is a root shell in the running container. The session is written to the audit log.</p>
      <div><button className="btn pri" onClick={() => setStarted(true)}>Open terminal</button></div>
    </div>
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
    <div className="col">
      <div className={`term-strip${env === "production" ? " prod" : ""}`}>
        <span>{env}</span>
        <button className="btn" onClick={onEnd}>End session</button>
      </div>
      {!open && !ended && <p className="muted">Connecting…</p>}
      {ended && <Callout tone="warn">{`Session ended: ${ended}${/[.!?]$/.test(ended) ? "" : "."}`}</Callout>}
      {ended && <div><button className="btn" onClick={() => (setEnded(""), setOpen(false), setAttempt((a) => a + 1))}>Reconnect</button></div>}
      <div ref={box} className="card" style={{ height: "60vh", padding: 8, background: "#000" }} />
    </div>
  );
}
