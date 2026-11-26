import { useState, type FormEvent } from "react";
import { Button, inputVariants } from "@cloudflare/kumo";
import { api } from "../api";
import { fragment } from "../router";
import { Callout, Field, errText, useBusy } from "../ui";
import { Centered } from "./Claim";
import { SetupStep, type Second } from "./SignIn";

export function Invite({ signedIn, onDone }: { signedIn?: string; onDone: (email: string) => void }) {
  const token = fragment();
  const [email, setEmail] = useState("");
  const [pw, setPw] = useState("");
  const [pw2, setPw2] = useState("");
  const [err, setErr] = useState("");
  const [setup, setSetup] = useState("");
  const [busy, run] = useBusy();

  async function submit(e: FormEvent) {
    e.preventDefault();
    if (pw !== pw2) return setErr("The two passwords differ.");
    setErr("");
    await run(async () => {
      try {
        const out = await api<{ joined: boolean } | Second>("POST", "/api/invites/accept", { token, email, password: pw });
        history.replaceState(null, "", "/");
        if ("second" in out) setSetup(out.ticket);
        else onDone(email.trim().toLowerCase());
      } catch (e) {
        setErr(errText(e));
      }
    });
  }

  if (setup) return <SetupStep ticket={setup} email={email.trim().toLowerCase()} onDone={onDone} />;
  return (
    <Centered title="Join this panel">
      {!token ? <p className="text-kumo-subtle">Open the invite link an Admin sent you.</p> : (
        <form className="flex flex-col gap-3" onSubmit={submit}>
          <Field label="Email">{(id) => <input id={id} className={inputVariants({ focusIndicator: true })} type="email" autoComplete="email" value={email} onChange={(e) => setEmail(e.target.value)} />}</Field>
          <Field label="Password" hint="12 characters or more.">{(id) => <input id={id} className={inputVariants({ focusIndicator: true })} type="password" autoComplete="new-password" minLength={12} required value={pw} onChange={(e) => setPw(e.target.value)} />}</Field>
          <Field label="Password again">{(id) => <input id={id} className={inputVariants({ focusIndicator: true })} type="password" autoComplete="new-password" minLength={12} required value={pw2} onChange={(e) => setPw2(e.target.value)} />}</Field>
          {err && <Callout tone="bad">{err}</Callout>}
          {signedIn && <p>{`You are signed in as ${signedIn}; joining signs you out.`}</p>}
          <Button type="submit" variant="primary" disabled={busy}>Join</Button>
        </form>
      )}
    </Centered>
  );
}
