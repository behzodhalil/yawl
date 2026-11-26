import { useState, type FormEvent } from "react";
import { Button, LayerCard, inputVariants } from "@cloudflare/kumo";
import { api } from "../api";
import { fragment } from "../router";
import { Callout, Field, errText } from "../ui";
import { RecoveryStep } from "./RecoveryStep";

export function Claim({ onDone }: { onDone: (email: string) => void }) {
  const token = fragment();
  const [email, setEmail] = useState("");
  const [pw, setPw] = useState("");
  const [pw2, setPw2] = useState("");
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);
  const [owner, setOwner] = useState("");

  if (owner) {
    return (
      <Centered title="One more step">
        <RecoveryStep onDone={() => onDone(owner)} />
      </Centered>
    );
  }
  if (!token) {
    return (
      <Centered title="This panel has no owner yet">
        <p className="text-kumo-subtle">Open the claim link that yawl server printed.</p>
      </Centered>
    );
  }

  async function submit(e: FormEvent) {
    e.preventDefault();
    if (pw !== pw2) return setErr("The two passwords differ.");
    setBusy(true);
    setErr("");
    try {
      const out = await api<{ email: string }>("POST", "/api/claim", { token, email, password: pw });
      history.replaceState(null, "", "/claim");
      const rec = await api<{ set: boolean }>("GET", "/api/recovery-key");
      if (rec.set) onDone(out.email);
      else setOwner(out.email);
    } catch (e) {
      setErr(errText(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Centered title="Claim this panel">
      <p className="text-kumo-subtle">You become its owner. There is one account; use a password of at least 12 characters.</p>
      <form className="flex flex-col gap-3" onSubmit={submit}>
        <Field label="Email">{(id) => <input id={id} className={inputVariants({ focusIndicator: true })} type="email" autoComplete="username" value={email} onChange={(e) => setEmail(e.target.value)} required />}</Field>
        <Field label="Password">{(id) => <input id={id} className={inputVariants({ focusIndicator: true })} type="password" autoComplete="new-password" value={pw} onChange={(e) => setPw(e.target.value)} required />}</Field>
        <Field label="Password again">{(id) => <input id={id} className={inputVariants({ focusIndicator: true })} type="password" autoComplete="new-password" value={pw2} onChange={(e) => setPw2(e.target.value)} required />}</Field>
        {err && <Callout tone="bad">{err}</Callout>}
        <Button type="submit" variant="primary" disabled={busy}>Claim this panel</Button>
      </form>
    </Centered>
  );
}

export function Centered({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <main className="grid min-h-full place-items-center px-4 py-6">
      <LayerCard className="flex w-full max-w-sm flex-col gap-3 p-6">
        <div className="flex items-center gap-2.5"><span className="grid size-5 place-items-center rounded bg-kumo-contrast font-mono text-xs text-kumo-inverse" aria-hidden="true">Y</span><b>Yawl</b></div>
        <h1>{title}</h1>
        {children}
      </LayerCard>
    </main>
  );
}
