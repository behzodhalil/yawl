import { Button, cn, inputVariants } from "@cloudflare/kumo";
import { useState } from "react";
import { api } from "./api";
import { dateTime } from "./format";
import { Callout, Modal, errText } from "./ui";

export function DeleteDialog({ what, path, purgeAt, users, usesRef, onDone, onClose }: {
  what: string; path: string; purgeAt: number; users?: string[]; usesRef?: string; onDone: () => void; onClose: () => void;
}) {
  const [typed, setTyped] = useState("");
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);
  return (
    <Modal title={`Delete ${what}?`} onClose={onClose}>
      <p>{`It stops now and is purged on ${dateTime(purgeAt)} unless restored.`}</p>
      {users && users.length > 0 && usesRef && <Callout tone="warn">{`${users.join(", ")} use it through ${usesRef}.`}</Callout>}
      <label className="flex flex-col gap-1">
        <span>{`Type ${what} to delete it.`}</span>
        <input className={cn(inputVariants({ focusIndicator: true }), "font-mono")} value={typed} onChange={(e) => setTyped(e.target.value)} autoComplete="off" />
      </label>
      {err && <Callout tone="bad">{err}</Callout>}
      <div className="flex flex-wrap items-center gap-2.5">
        <Button variant="destructive" disabled={typed !== what || busy} onClick={async () => {
          setBusy(true); setErr("");
          try { await api("DELETE", path, { confirm: typed }); onDone(); } catch (e) { setErr(errText(e)); setBusy(false); }
        }}>{`Delete ${what}`}</Button>
        <Button variant="ghost" onClick={onClose}>Cancel</Button>
      </div>
    </Modal>
  );
}
