import { useState, type FormEvent } from "react";
import { Button, inputVariants } from "@cloudflare/kumo";
import { api } from "../api";
import { fragment } from "../router";
import { Callout, Field, errText, useBusy } from "../ui";
import { Centered } from "./Claim";

export function Reset({ onDone }: { onDone: () => void }) {
  const token = fragment();
  const [pw, setPw] = useState("");
  const [pw2, setPw2] = useState("");
  const [err, setErr] = useState("");
  const [busy, run] = useBusy();

  async function submit(e: FormEvent) {
    e.preventDefault();
    if (pw !== pw2) return setErr("The two passwords differ.");
    setErr("");
    await run(async () => {
      try {
        await api("POST", "/api/password-reset", { token, password: pw });
        history.replaceState(null, "", "/reset");
        onDone();
      } catch (e) {
        setErr(errText(e));
      }
    });
  }

  return (
    <Centered title="Set a new password">
      {!token ? <p className="text-kumo-subtle">Open the link yawl server reset-password printed.</p> : (
        <form className="flex flex-col gap-3" onSubmit={submit}>
          <Field label="New password" hint="12 characters or more.">{(id) => <input id={id} className={inputVariants({ focusIndicator: true })} type="password" autoComplete="new-password" minLength={12} required value={pw} onChange={(e) => setPw(e.target.value)} />}</Field>
          <Field label="New password again">{(id) => <input id={id} className={inputVariants({ focusIndicator: true })} type="password" autoComplete="new-password" minLength={12} required value={pw2} onChange={(e) => setPw2(e.target.value)} />}</Field>
          {err && <Callout tone="bad">{err}</Callout>}
          <Button type="submit" variant="primary" disabled={busy}>Set password</Button>
        </form>
      )}
    </Centered>
  );
}
