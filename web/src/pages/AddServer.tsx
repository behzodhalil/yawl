import { Suspense, lazy, useContext, useState } from "react";
import { api } from "../api";
import { SessionContext } from "../session";
import { Shell } from "../Shell";
import { Button, LayerCard } from "@cloudflare/kumo";
import { Callout, Link, errText, useBusy } from "../ui";
import { Hetzner } from "./Hetzner";

const Command = lazy(() => import("../blocks/command"));

export function JoinCommand({ intro }: { intro: string }) {
  const [cmd, setCmd] = useState("");
  const [err, setErr] = useState("");
  const [busy, run] = useBusy();
  return (
    <LayerCard className="flex max-w-[720px] flex-col gap-3 p-4">
      <p>{intro}</p>
      {cmd ? (
        <>
          <Suspense fallback={<code className="block rounded-sm bg-kumo-recessed p-4 font-mono text-sm break-all ring ring-kumo-hairline">{cmd}</code>}>
            <Command code={cmd} />
          </Suspense>
          <p className="text-kumo-subtle">Good for one hour and one use.</p>
        </>
      ) : (
        <div>
          <Button variant="primary" disabled={busy} onClick={() => run(async () => {
            try {
              setCmd((await api<{ command: string }>("POST", "/api/join-token")).command);
            } catch (e) {
              setErr(errText(e));
            }
          })}>Make the join command</Button>
        </div>
      )}
      {err && <Callout tone="bad">{err}</Callout>}
    </LayerCard>
  );
}

export const MANAGED_NEEDS = "A server needs Ubuntu 22.04 to 26.04 or Debian 12, ports 80 and 443 open, and 1 GB of memory.";

export function AddServer() {
  const managed = useContext(SessionContext).managed;
  return (
    <Shell crumb={[<Link to="/servers">Servers</Link>, "Add server"]} active="/servers">
      <h1>Add server</h1>
      <JoinCommand intro={managed ? `${MANAGED_NEEDS} Run the command on it; the server appears in the list within a minute.`
        : "Any Linux box with Docker works: Hetzner, AWS, Alibaba Cloud or your own. Run the command on it; the server appears in the list within seconds."} />
      {!managed && <Hetzner />}
    </Shell>
  );
}
