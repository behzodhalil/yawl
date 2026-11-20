import { useEffect, useState, type FormEvent } from "react";
import { api } from "../api";
import { TwoStepSetup, type SetupData } from "../TwoStepSetup";
import { Callout, Field, errText, useBusy } from "../ui";
import { Centered } from "./Claim";

export type Second = { second: "code" | "setup"; ticket: string };

const SSO_WHY: Record<string, string> = {
  unknown: "That account is not a member of this team. Ask an Admin for an invite.",
  unverified: "The provider did not say the email is verified.",
  expired: "That sign-in took too long or was started elsewhere. Try again.",
  refused: "The provider did not confirm the sign-in. Try again.",
  off: "SSO is not set up any more.",
  error: "Something went wrong. Try again.",
  other: "That provider account is not the one this member signs in with.",
  busy: "Too many sign-ins are under way. Try again in a minute.",
};

export function SignIn({ onDone }: { onDone: (email: string) => void }) {
  const [sso, setSso] = useState<{ name?: string; only?: boolean }>({});
  useEffect(() => { api<{ name?: string; only?: boolean }>("GET", "/api/session/sso").then(setSso, () => {}); }, []);
  const ssoParam = new URLSearchParams(location.search).get("sso") ?? "";
  const why = SSO_WHY[ssoParam];
  const reset = new URLSearchParams(location.search).get("reset") === "1";
  const [ssoTicket, setSsoTicket] = useState(() => ssoParam === "code" ? new URLSearchParams(location.hash.slice(1)).get("t") ?? "" : "");
  const [email, setEmail] = useState("");
  const [pw, setPw] = useState("");
  const [err, setErr] = useState("");
  const [busy, run] = useBusy();
  const [next, setNext] = useState<Second | null>(null);

  async function submit(e: FormEvent) {
    e.preventDefault();
    setErr("");
    await run(async () => {
      try {
        const out = await api<{ email: string } | Second>("POST", "/api/session", { email, password: pw });
        if ("second" in out) setNext(out);
        else onDone(out.email);
      } catch (e) {
        setErr(errText(e));
      }
    });
  }

  const again = () => (history.replaceState(null, "", location.pathname), setSsoTicket(""), setNext(null));
  if (ssoTicket) return <CodeStep ticket={ssoTicket} onDone={onDone} again={again} />;
  if (next?.second === "code") return <CodeStep ticket={next.ticket} onDone={onDone} again={again} />;
  if (next?.second === "setup") return <SetupStep ticket={next.ticket} email={email.trim().toLowerCase()} onDone={onDone} />;
  return (
    <Centered title="Sign in">
      {reset && <Callout tone="ok">Password changed. Sign in with the new one.</Callout>}
      {why && <Callout tone="bad">{why}</Callout>}
      {sso.name && <a className="btn pri" href="/auth/sso/start">{`Sign in with ${sso.name}`}</a>}
      {sso.only && <p className="muted">Password sign-in is only for the oldest Admin.</p>}
      <form className="col" onSubmit={submit}>
        <Field label="Email">{(id) => <input id={id} className="in" type="email" autoComplete="username" value={email} onChange={(e) => setEmail(e.target.value)} required />}</Field>
        <Field label="Password">{(id) => <input id={id} className="in" type="password" autoComplete="current-password" value={pw} onChange={(e) => setPw(e.target.value)} required />}</Field>
        {err && <Callout tone="bad">{err}</Callout>}
        <button className="btn pri" disabled={busy}>Sign in</button>
      </form>
    </Centered>
  );
}

function CodeStep({ ticket, onDone, again }: { ticket: string; onDone: (email: string) => void; again: () => void }) {
  const [code, setCode] = useState("");
  const [recovery, setRecovery] = useState(false);
  const [err, setErr] = useState("");
  const [busy, run] = useBusy();

  async function submit(e: FormEvent) {
    e.preventDefault();
    setErr("");
    await run(async () => {
      try {
        onDone((await api<{ email: string }>("POST", "/api/session/code", { ticket, code })).email);
      } catch (e) {
        setErr(errText(e));
      }
    });
  }

  return (
    <Centered title="Sign in">
      <form className="col" onSubmit={submit}>
        <p>{recovery ? "Enter one of your recovery codes." : "Enter the code from your authenticator app."}</p>
        <Field label={recovery ? "Recovery code" : "Code"}>
          {(id) => <input id={id} className="in mono" autoComplete="one-time-code" inputMode={recovery ? "text" : "numeric"} value={code} onChange={(e) => setCode(e.target.value)} required autoFocus />}
        </Field>
        {err && <Callout tone="bad">{err}</Callout>}
        <button className="btn pri" disabled={busy}>Continue</button>
        <button type="button" className="btn ghost" onClick={() => (setRecovery(!recovery), setCode(""))}>
          {recovery ? "Use the app's code" : "Use a recovery code"}
        </button>
        <button type="button" className="btn ghost" onClick={again}>Start again</button>
      </form>
    </Centered>
  );
}

export function SetupStep({ ticket, email, onDone }: { ticket: string; email: string; onDone: (email: string) => void }) {
  return (
    <Centered title="Turn on two-step sign in">
      <p className="muted">This panel asks everyone for a code from an authenticator app after the password.</p>
      <TwoStepSetup
        start={() => api<SetupData>("POST", "/api/session/setup", { ticket })}
        confirm={(code) => api<{ recovery_codes: string[] }>("POST", "/api/session/setup/confirm", { ticket, code })}
        onDone={() => onDone(email)} />
    </Centered>
  );
}
