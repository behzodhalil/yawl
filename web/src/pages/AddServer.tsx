import { useContext, useState } from "react";
import { api } from "../api";
import { SessionContext } from "../session";
import { Shell } from "../Shell";
import { Callout, Copy, Link, errText, useBusy } from "../ui";
import { Hetzner } from "./Hetzner";

export function JoinCommand({ intro }: { intro: string }) {
  const [cmd, setCmd] = useState("");
  const [err, setErr] = useState("");
  const [busy, run] = useBusy();
  return (
    <div className="card pad col w-form">
      <p>{intro}</p>
      {cmd ? (
        <>
          <div className="card pad row" style={{ justifyContent: "space-between", flexWrap: "nowrap" }}>
            <code style={{ wordBreak: "break-all" }}>{cmd}</code>
            <Copy text={cmd} />
          </div>
          <p className="muted">Good for one hour and one use.</p>
        </>
      ) : (
        <div>
          <button className="btn pri" disabled={busy} onClick={() => run(async () => {
            try {
              setCmd((await api<{ command: string }>("POST", "/api/join-token")).command);
            } catch (e) {
              setErr(errText(e));
            }
          })}>Make the join command</button>
        </div>
      )}
      {err && <Callout tone="bad">{err}</Callout>}
    </div>
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
