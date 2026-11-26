import { useContext, useState } from "react";
import { api } from "../api";
import { Shell } from "../Shell";
import { SessionContext } from "../session";
import { Button, LayerCard } from "@cloudflare/kumo";
import { Callout, Loading, errText, useBusy, useLoad } from "../ui";

export function Adopt() {
  const view = useLoad<{ adopting: boolean; lines: string[]; seen: string }>("/api/adopt");
  const [err, setErr] = useState("");
  const [done, setDone] = useState(false);
  const [busy, run] = useBusy();
  const { reload } = useContext(SessionContext);
  const missing = (view.data?.lines ?? []).filter((l) => l.endsWith(" has not connected yet.")).map((l) => l.replace(" has not connected yet.", ""));
  const accept = (without: string[]) => run(async () => {
    setErr("");
    try {
      await api("POST", "/api/adopt", { without, seen: view.data?.seen });
      setDone(true);
      reload?.();
    } catch (e) {
      setErr(errText(e));
      view.reload();
    }
  });
  return (
    <Shell crumb={["Review the servers"]} active="">
      <h1>Review the servers</h1>
      {!view.data ? <Loading what={view} /> : done || !view.data.adopting ? (
        <Callout tone="ok" title={done ? "Adopted. The panel acts on the servers again." : "Nothing to review."} />
      ) : (
        <LayerCard className="flex max-w-[720px] flex-col gap-3 p-4">
          <p className="text-kumo-subtle">This panel came back from a backup and may be behind the servers. Nothing was stopped or redeployed. Accepting records what the servers run as the panel's own; nothing is deleted.</p>
          {view.data.lines.length === 0 ? <p>The servers run what the backup knew.</p> : view.data.lines.map((l) => <code key={l}>{l}</code>)}
          {err && <Callout tone="bad">{err}</Callout>}
          <div className="flex flex-wrap items-center gap-2.5">
            <Button variant="primary" disabled={busy} onClick={() => accept([])}>Accept what the servers run</Button>
            {missing.length > 0 && <Button disabled={busy} onClick={() => accept(missing)}>{`Accept without ${missing.join(", ")}`}</Button>}
          </div>
          {missing.length > 0 && <p className="text-kumo-subtle">Accept without a server only if that machine is gone, for example the one this panel ran on. Its apps are not touched; join a new server for them.</p>}
        </LayerCard>
      )}
    </Shell>
  );
}
