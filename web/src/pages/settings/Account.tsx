import { useState, type FormEvent } from "react";
import { api } from "../../api";
import { navigate } from "../../router";
import { RecoveryCodes, TwoStepSetup, type SetupData } from "../../TwoStepSetup";
import { Button } from "@cloudflare/kumo";
import { Callout, Card, Field, errText, useBusy, useLoad, Input } from "../../ui";

export function Account() {
  const s = useLoad<{ email?: string; role?: string }>("/api/me");
  const [old, setOld] = useState("");
  const [pw, setPw] = useState("");
  const [pw2, setPw2] = useState("");
  const [msg, setMsg] = useState<[string, string] | null>(null);
  const [busy, run] = useBusy();

  async function change(e: FormEvent) {
    e.preventDefault();
    if (pw !== pw2) return setMsg(["bad", "The two new passwords differ."]);
    await run(async () => {
      try {
        await api("PUT", "/api/account/password", { old, new: pw });
        setOld(""); setPw(""); setPw2("");
        setMsg(["ok", "Changed. Every other session was signed out."]);
      } catch (e) {
        setMsg(["bad", errText(e)]);
      }
    });
  }

  return (
    <div className="flex max-w-[var(--w-form)] flex-col gap-3">
      <Card title="Signed in">
        <div className="flex flex-wrap items-center justify-between gap-2.5">
          <span>Signed in as <b>{s.data?.email}</b>{s.data?.role && `, ${/^[aeiou]/.test(s.data.role) ? "an" : "a"} ${s.data.role}`}.</span>
          <Button onClick={async () => { await api("DELETE", "/api/session").catch(() => {}); navigate("/sign-in"); }}>Sign out</Button>
        </div>
      </Card>
      <Card title="Password">
      <p className="text-kumo-subtle">At least 12 characters. Changing it signs out every other session.</p>
      <form className="flex flex-col gap-3" onSubmit={change}>
        <Field label="Current password">{(id) => <Input id={id} type="password" autoComplete="current-password" value={old} onChange={(e) => setOld(e.target.value)} required />}</Field>
        <Field label="New password">{(id) => <Input id={id} type="password" autoComplete="new-password" value={pw} onChange={(e) => setPw(e.target.value)} required />}</Field>
        <Field label="New password again">{(id) => <Input id={id} type="password" autoComplete="new-password" value={pw2} onChange={(e) => setPw2(e.target.value)} required />}</Field>
        {msg && <Callout tone={msg[0] as "ok" | "bad"}>{msg[1]}</Callout>}
        <div><Button type="submit" variant="primary" loading={busy} disabled={!old || !pw || !pw2}>Change password</Button></div>
      </form>
      </Card>
      <TwoStep />
    </div>
  );
}

type Status = { on: boolean; codes_left: number; required: boolean };

function TwoStep() {
  const st = useLoad<Status>("/api/account/two-step");
  const [mode, setMode] = useState<"" | "setup" | "codes" | "off" | "move">("");
  const [code, setCode] = useState("");
  const [codes, setCodes] = useState<string[] | null>(null);
  const [err, setErr] = useState("");
  const [moveCode, setMoveCode] = useState("");
  const done = () => (setMode(""), setCode(""), setMoveCode(""), setCodes(null), setErr(""), st.reload());

  async function act(e: FormEvent) {
    e.preventDefault();
    setErr("");
    try {
      if (mode === "move") return (setMoveCode(code), setCode(""), setMode("setup"));
      if (mode === "codes") setCodes((await api<{ recovery_codes: string[] }>("POST", "/api/account/two-step/codes", { code })).recovery_codes);
      else (await api("DELETE", "/api/account/two-step", { code }), done());
    } catch (e) {
      setErr(errText(e));
    }
  }

  const s = st.data;
  return (
    <Card title="Two-step sign-in">
      <p className="text-kumo-subtle">A code from an app on your phone at every sign-in.</p>
      {mode === "setup" ? (
        <TwoStepSetup
          start={() => api<SetupData>("POST", "/api/account/two-step", moveCode ? { code: moveCode } : undefined)}
          confirm={(c) => api<{ recovery_codes: string[] }>("PUT", "/api/account/two-step", { code: c })}
          onDone={done} />
      ) : codes ? <RecoveryCodes codes={codes} onDone={done} /> : !s ? null : !s.on ? (
        <>
          <p>Off.{s.required ? " This panel requires it; you will set it up at your next sign-in." : ""}</p>
          <div><Button onClick={() => setMode("setup")}>Turn on</Button></div>
        </>
      ) : (
        <>
          <p>{`On. ${s.codes_left} recovery code${s.codes_left === 1 ? "" : "s"} left.`}</p>
          {mode === "" ? (
            <div className="flex flex-wrap items-center gap-2.5">
              <Button onClick={() => setMode("move")}>Move to a new phone</Button>
              <Button onClick={() => setMode("codes")}>New recovery codes</Button>
              {!s.required && <Button variant="ghost" onClick={() => setMode("off")}>Turn off</Button>}
            </div>
          ) : (
            <form className="flex flex-col gap-3" onSubmit={act}>
              <Field label="Code" hint="From your authenticator app, or a recovery code.">
                {(id) => <Input id={id} className="font-mono text-sm" autoComplete="one-time-code" value={code} onChange={(e) => setCode(e.target.value)} required />}
              </Field>
              <div className="flex flex-wrap items-center gap-2.5">
                <Button type="submit" variant="primary">{mode === "codes" ? "Make new codes" : mode === "move" ? "Continue" : "Turn off"}</Button>
                <Button variant="ghost" onClick={done}>Cancel</Button>
              </div>
            </form>
          )}
        </>
      )}
      {err && <Callout tone="bad">{err}</Callout>}
    </Card>
  );
}
