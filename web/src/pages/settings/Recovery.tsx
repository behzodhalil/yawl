import { useState } from "react";
import { Button } from "@cloudflare/kumo";
import { Loading, useLoad, Card } from "../../ui";
import { RecoveryStep } from "../RecoveryStep";

export function Recovery() {
  const cur = useLoad<{ set: boolean; fingerprint?: string }>("/api/recovery-key");
  const [making, setMaking] = useState(false);
  if (!cur.data) return <Loading what={cur} />;
  return (
    <Card title="Recovery key" className="max-w-[var(--w-form)]">
      {cur.data.set ? (
        <>
          <p>The recovery key exists. Its public half begins <code>{cur.data.fingerprint}</code>.</p>
          <p className="text-kumo-subtle">Only you hold this key. You need it to restore a backup or move secrets to a new server, and it cannot be replaced.</p>
        </>
      ) : making ? (
        <RecoveryStep onDone={() => (setMaking(false), cur.reload())} />
      ) : (
        <>
          <p>There is no recovery key yet, so no secret can be set and no backup can run.</p>
          <div><Button variant="primary" onClick={() => setMaking(true)}>Make the recovery key</Button></div>
        </>
      )}
    </Card>
  );
}
