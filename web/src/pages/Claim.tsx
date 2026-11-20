import { useState, type FormEvent } from "react";
import { api } from "../api";
import { Callout, Field, errText } from "../ui";
import { RecoveryStep } from "./RecoveryStep";

export function Claim({ onDone }: { onDone: (email: string) => void }) {
  const token = location.hash.slice(1);
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
        <p className="muted">Open the claim link that yawl server printed.</p>
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
      <p className="muted">You become its owner. There is one account; use a password of at least 12 characters.</p>
      <form className="col" onSubmit={submit}>
        <Field label="Email">{(id) => <input id={id} className="in" type="email" autoComplete="username" value={email} onChange={(e) => setEmail(e.target.value)} required />}</Field>
        <Field label="Password">{(id) => <input id={id} className="in" type="password" autoComplete="new-password" value={pw} onChange={(e) => setPw(e.target.value)} required />}</Field>
        <Field label="Password again">{(id) => <input id={id} className="in" type="password" autoComplete="new-password" value={pw2} onChange={(e) => setPw2(e.target.value)} required />}</Field>
        {err && <Callout tone="bad">{err}</Callout>}
        <button className="btn pri" disabled={busy}>Claim this panel</button>
      </form>
    </Centered>
  );
}

export function Centered({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <main className="centered">
      <div className="card pad col" style={{ width: "min(420px, 100%)" }}>
        <div className="row"><span className="mark" aria-hidden="true">Y</span><b>Yawl</b></div>
        <h1>{title}</h1>
        {children}
      </div>
    </main>
  );
}
