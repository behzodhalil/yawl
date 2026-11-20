import { useState } from "react";
import { Loading, useLoad } from "../../ui";
import { RecoveryStep } from "../RecoveryStep";

export function Recovery() {
  const cur = useLoad<{ set: boolean; fingerprint?: string }>("/api/recovery-key");
  const [making, setMaking] = useState(false);
  if (!cur.data) return <Loading what={cur} />;
  return (
    <div className="card pad col" style={{ maxWidth: 640 }}>
      <h2>Recovery key</h2>
      {cur.data.set ? (
        <>
          <p>The recovery key exists. Its public half begins <code>{cur.data.fingerprint}</code>.</p>
          <p className="muted">Only you hold this key. You need it to restore a backup or move secrets to a new server, and it cannot be replaced.</p>
        </>
      ) : making ? (
        <RecoveryStep onDone={() => (setMaking(false), cur.reload())} />
      ) : (
        <>
          <p>There is no recovery key yet, so no secret can be set and no backup can run.</p>
          <div><button className="btn pri" onClick={() => setMaking(true)}>Make the recovery key</button></div>
        </>
      )}
    </div>
  );
}
