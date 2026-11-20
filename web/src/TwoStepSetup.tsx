import { useEffect, useRef, useState, type FormEvent } from "react";
import { Callout, Copy, Field, errText } from "./ui";

export type SetupData = { secret: string; uri: string; qr: string };

export function RecoveryCodes({ codes, onDone }: { codes: string[]; onDone: () => void }) {
  return (
    <div className="col">
      <p>Recovery codes. Each signs you in once without the app. Keep them somewhere other than your phone; they are not shown again.</p>
      <div className="card pad mono" style={{ display: "grid", gridTemplateColumns: "repeat(2, max-content)", gap: "4px 24px" }}>
        {codes.map((c) => <span key={c}>{c}</span>)}
      </div>
      <div className="row">
        <Copy text={codes.join("\n")} />
        <button type="button" className="btn pri" onClick={onDone}>I saved them</button>
      </div>
    </div>
  );
}

export function TwoStepSetup({ start, confirm, onDone }: {
  start: () => Promise<SetupData>;
  confirm: (code: string) => Promise<{ recovery_codes: string[] }>;
  onDone: () => void;
}) {
  const [data, setData] = useState<SetupData | null>(null);
  const [code, setCode] = useState("");
  const [codes, setCodes] = useState<string[] | null>(null);
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);

  const startOnMount = useRef(start);
  useEffect(() => {
    startOnMount.current().then(setData, (e) => setErr(errText(e)));
  }, []);

  async function submit(e: FormEvent) {
    e.preventDefault();
    setErr("");
    setBusy(true);
    try {
      setCodes((await confirm(code)).recovery_codes);
    } catch (e) {
      setErr(errText(e));
    } finally {
      setBusy(false);
    }
  }

  if (codes) return <RecoveryCodes codes={codes} onDone={onDone} />;
  return (
    <form className="col" onSubmit={submit}>
      {data && (
        <>
          <p>Scan this with your authenticator app, or type the key into it.</p>
          <img alt="QR code for your authenticator app" width={180} height={180}
            src={"data:image/svg+xml;utf8," + encodeURIComponent(data.qr)} />
          <code className="mono">{data.secret}</code>
          <Field label="Code" hint="The 6 digits the app shows now.">
            {(id) => <input id={id} className="in mono" inputMode="numeric" autoComplete="one-time-code" value={code} onChange={(e) => setCode(e.target.value)} required />}
          </Field>
          <div><button className="btn pri" disabled={busy}>Turn on</button></div>
        </>
      )}
      {err && <Callout tone="bad">{err}</Callout>}
    </form>
  );
}
