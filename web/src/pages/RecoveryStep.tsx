import { useEffect, useState } from "react";
import { api } from "../api";
import { backupKey, newRecoveryKey } from "../seal";
import { Callout, Copy, errText } from "../ui";

export function RecoveryStep({ onDone }: { onDone: () => void }) {
  const [key, setKey] = useState<{ text: string; pub: string } | null>(null);
  const [saved, setSaved] = useState(false);
  const [err, setErr] = useState("");
  useEffect(() => {
    newRecoveryKey().then(setKey);
  }, []);
  if (!key) return <p className="muted">Making the recovery key…</p>;
  return (
    <div className="col">
      <h2>Your recovery key</h2>
      <p className="muted">
        It is shown once. You need it to restore a backup and to move secrets to a new server. Yawl keeps only its public half, so it cannot show it again.
      </p>
      <div className="card pad row" style={{ justifyContent: "space-between" }}>
        <code style={{ wordBreak: "break-all" }}>{key.text}</code>
        <Copy text={key.text} />
      </div>
      <label className="row" style={{ gap: 8 }}>
        <input type="checkbox" checked={saved} onChange={(e) => setSaved(e.target.checked)} />
        I saved the recovery key somewhere other than this server
      </label>
      {err && <Callout tone="bad">{err}</Callout>}
      <button
        className="btn pri"
        disabled={!saved}
        onClick={async () => {
          try {
            await api("PUT", "/api/recovery-key", { pubkey: key.pub, backup_key: await backupKey(key.text) });
            onDone();
          } catch (e) {
            setErr(errText(e));
          }
        }}
      >
        Continue
      </button>
    </div>
  );
}
