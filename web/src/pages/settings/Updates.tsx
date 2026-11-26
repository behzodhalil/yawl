import { useEffect, useState } from "react";
import { Button, Loader } from "@cloudflare/kumo";
import { api } from "../../api";
import { Callout, Loading, Modal, errText, useLoad, Card } from "../../ui";

type State = { current: string; latest?: string; error?: string };

export function Updates() {
  const cur = useLoad<State>("/api/update");
  const [phase, setPhase] = useState<"" | "restarting" | "done" | "lost">("");
  const [target, setTarget] = useState("");
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);
  const [asking, setAsking] = useState(false);
  const [noBackup, setNoBackup] = useState("");
  useEffect(() => {
    if (phase !== "restarting") return;
    const t = setInterval(async () => {
      try {
        const s = await api<State>("GET", "/api/update");
        if (s.current === target) setPhase("done");
      } catch {}
    }, 2000);
    const giveUp = setTimeout(() => setPhase("lost"), 180_000);
    return () => (clearInterval(t), clearTimeout(giveUp));
  }, [phase, target]);
  if (!cur.data) return <Loading what={cur} />;
  const s = cur.data;
  const newer = s.latest && s.latest !== s.current;
  const backupFailed = err.startsWith("The backup before the update failed");
  const run = async (skipBackup: boolean) => {
    setErr("");
    setAsking(false);
    setBusy(true);
    try {
      const r = await api<{ version: string; backup?: string }>("POST", "/api/update", skipBackup ? { version: s.latest, skip_backup: true } : { version: s.latest });
      setNoBackup(r.backup?.startsWith("none: ") ? r.backup.slice(6) : "");
      setTarget(r.version);
      setPhase("restarting");
    } catch (e) {
      setErr(errText(e));
    } finally {
      setBusy(false);
    }
  };
  return (
    <Card title="Updates" className="max-w-[var(--w-form)]">
      <p>This panel runs <code>{s.current === "dev" ? "dev" : "v" + s.current}</code>.</p>
      {s.error && <Callout tone="warn">{s.error}</Callout>}
      {phase === "restarting" && <Callout tone="warn" title="The panel is restarting…">Apps keep running. This page notices when it is back.</Callout>}
      {phase === "lost" && <Callout tone="bad">{`The panel did not come back on ${target}. Check the server with yawl status.`}</Callout>}
      {phase && noBackup && <Callout tone="warn">{`No backup was taken: ${noBackup}.`}</Callout>}
      {phase === "done" && <Callout tone="ok" title={`The panel runs v${target}.`}>Agents update themselves at their next connect.</Callout>}
      {!phase && newer && (
        <>
          <p>{`v${s.latest} is out.`}</p>
          <div className="flex flex-wrap items-center gap-2.5">
            <Button icon={busy ? <span aria-hidden="true" className="inline-flex"><Loader size={14} /></span> : undefined} variant="primary" disabled={busy} onClick={() => run(false)}>{busy ? "Backing up and updating…" : `Update to v${s.latest}`}</Button>
            {backupFailed && <Button variant="secondary-destructive" disabled={busy} onClick={() => setAsking(true)}>Update without a backup</Button>}
          </div>
        </>
      )}
      {!phase && !newer && !s.error && <p className="text-kumo-subtle">This is the latest release.</p>}
      {err && <Callout tone="bad">{err}</Callout>}
      {asking && (
        <Modal title={`Update to v${s.latest} with no backup?`} onClose={() => setAsking(false)}>
          <p>If the update goes wrong, there is no copy of the panel from just before it to restore.</p>
          <div className="flex flex-wrap items-center gap-2.5">
            <Button variant="destructive" onClick={() => run(true)}>Update with no backup</Button>
            <Button variant="ghost" onClick={() => setAsking(false)}>Cancel</Button>
          </div>
        </Modal>
      )}
    </Card>
  );
}
