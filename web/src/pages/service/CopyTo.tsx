import { useEffect, useState } from "react";
import { api } from "../../api";
import { reseal } from "../../reseal";
import type { ProjectView, SealedRow } from "../../types";
import { Callout, Confirm, Field, Link, errText, useBusy, useLoad } from "../../ui";

type Plan = { lines: string[]; to_pubkey?: string; recovery_pubkey?: string; vars: SealedRow[] };

export function CopyTo({ project, env, service }: { project: string; env: string; service: string }) {
  const path = `/api/services/${project}/${env}/${service}/copy`;
  const tree = useLoad<ProjectView[]>("/api/projects");
  const [to, setTo] = useState("");
  const [plan, setPlan] = useState<Plan | null>(null);
  const [key, setKey] = useState("");
  const [err, setErr] = useState("");
  const [done, setDone] = useState("");
  const [moving, setMoving] = useState(false);
  const [, run] = useBusy();
  const [p, e] = to.split("/");
  useEffect(() => {
    setPlan(null), setErr("");
    if (!to) return;
    api<Plan>("POST", path + "?dry=1", { project: p, env: e }).then(setPlan, (x) => setErr(errText(x)));
  }, [path, to, p, e]);
  async function go(move: boolean) {
    setErr("");
    let boxes: Record<string, { agent_box: string; updated_at: number }> = {};
    if (plan && plan.vars.length > 0) {
      boxes = (await reseal({ lines: plan.lines, to_pubkey: plan.to_pubkey!, recovery_pubkey: plan.recovery_pubkey!, restore: false, vars: plan.vars }, key)).boxes;
      setKey("");
    }
    const out = await api<{ path: string }>("POST", path, { project: p, env: e, move, boxes, to_pubkey: plan?.to_pubkey });
    setDone(out.path);
  }
  const targets = (tree.data ?? []).flatMap((pr) => pr.envs.map((x) => `${pr.name}/${x.name}`)).filter((t) => t !== `${project}/${env}`);
  return (
    <div className="col">
      <Field label="To">{(id) => (
        <select id={id} className="in" value={to} onChange={(x) => setTo(x.target.value)}>
          <option value="">Choose an environment</option>
          {targets.map((t) => <option key={t} value={t}>{t}</option>)}
        </select>
      )}</Field>
      {plan && <div className="col" style={{ gap: 4 }}>{plan.lines.map((l) => <code key={l}>{l}</code>)}</div>}
      {plan && plan.vars.length > 0 && (
        <Field label="Recovery key" hint="Opens its variables here, in this browser, to seal them for the other server.">
          {(id) => <textarea id={id} className="in mono" rows={2} style={{ height: "auto", padding: 8 }} autoComplete="off" spellCheck={false} value={key} onChange={(x) => setKey(x.target.value)} />}
        </Field>
      )}
      {err && <Callout tone="bad">{err}</Callout>}
      {done ? <Link to={done}>Open the copy</Link> : plan && (
        <div className="row">
          <button className="btn" onClick={() => run(() => go(false).catch((x) => setErr(errText(x))))}>Copy</button>
          <button className="btn ghost" onClick={() => setMoving(true)}>Move (the original goes to the trash)</button>
        </div>
      )}
      {moving && (
        <Confirm title={`Move ${service} to ${to}?`} yes="Yes, move" onYes={() => go(true)} onClose={() => setMoving(false)}>
          <p>The original goes to the trash.</p>
        </Confirm>
      )}
    </div>
  );
}
