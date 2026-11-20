import { useEffect, useState } from "react";
import { api } from "../api";
import { Shell } from "../Shell";
import { ago } from "../format";
import { Callout, Skeleton, errText } from "../ui";

export function CliLogin() {
  const code = location.hash.slice(1);
  const [who, setWho] = useState<{ host: string; from: string; at: number; approved: boolean } | null>(null);
  const [err, setErr] = useState("");
  const [check, setCheck] = useState("");
  useEffect(() => {
    if (!code) return;
    api<{ host: string; from: string; at: number; approved: boolean }>("GET", `/api/cli-login/${encodeURIComponent(code)}/info`).then(setWho, (e) => setErr(errText(e)));
  }, [code]);
  return (
    <Shell crumb={["CLI login"]} active="">
      <h1>Approve a CLI login</h1>
      <div className="card pad col" style={{ maxWidth: 560 }}>
        {!code ? <p className="muted">Open the link that yawl login printed.</p> : check ? (
          <>
            <p>Type this code into the terminal that printed the link:</p>
            <code style={{ fontSize: 18 }}>{check}</code>
            <p className="muted">Never send it to anyone: whoever types it signs in as you.</p>
          </>
        ) : err ? <Callout tone="bad">{err}</Callout> : !who ? <Skeleton /> : who.approved ? (
          <Callout tone="ok" title="Already approved. If the terminal still waits, run yawl login again." />
        ) : (
          <>
            <p>{`A computer at ${who.from}, calling itself ${who.host}, asked ${ago(who.at)}.`}</p>
            <p>It asks for an API token.</p>
            <p className="muted">Approve only a login you started. The token can do anything this dashboard can, except change the password.</p>
            <div><button className="btn pri" onClick={async () => {
              try { setCheck((await api<{ check: string }>("POST", `/api/cli-login/${encodeURIComponent(code)}/approve`)).check); } catch (e) { setErr(errText(e)); }
            }}>Approve</button></div>
          </>
        )}
      </div>
    </Shell>
  );
}
