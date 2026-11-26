import { useEffect, useState } from "react";
import { Button, Checkbox, ClipboardText } from "@cloudflare/kumo";
import { api } from "../api";
import { backupKey, newRecoveryKey } from "../seal";
import { Callout, errText } from "../ui";

export function RecoveryStep({ onDone }: { onDone: () => void }) {
  const [key, setKey] = useState<{ text: string; pub: string } | null>(null);
  const [saved, setSaved] = useState(false);
  const [err, setErr] = useState("");
  useEffect(() => {
    newRecoveryKey().then(setKey);
  }, []);
  if (!key) return <p className="text-kumo-subtle">Making the recovery key…</p>;
  return (
    <div className="flex flex-col gap-3">
      <h2>Your recovery key</h2>
      <p className="text-kumo-subtle">
        It is shown once. You need it to restore a backup and to move secrets to a new server. Yawl keeps only its public half, so it cannot show it again.
      </p>
      <ClipboardText className="max-w-full" text={key.text} />
      <Checkbox label="I saved the recovery key somewhere other than this server" checked={saved} onCheckedChange={(c) => setSaved(!!c)} />
      {err && <Callout tone="bad">{err}</Callout>}
      <Button
        variant="primary"
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
      </Button>
    </div>
  );
}
