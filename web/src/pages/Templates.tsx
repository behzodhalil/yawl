import { isDb } from "../serviceForm";
import { useState } from "react";
import { api } from "../api";
import { randomSecret, setRef, setVar } from "../seal";
import { Shell } from "../Shell";
import type { Change, ServiceForm } from "../types";
import { Callout, Copy, Field, Link, Loading, errText, useLoad } from "../ui";
import { PlanBox } from "./NewService";

type Card = { id: string; title: string; line: string; memory: string; icons: string[] };
type Var = { service: string; key: string; kind: "fixed" | "generate" | "ask" | "ref"; value?: string; label?: string };
type Template = Card & { note: string; services: ServiceForm[]; vars: Var[] };

const wait = (ms: number) => new Promise((r) => setTimeout(r, ms));

export function Templates({ project, env, poll = 2000, deadline = 10 * 60_000 }: { project: string; env: string; poll?: number; deadline?: number }) {
  const list = useLoad<Card[]>("/api/templates");
  const [t, setT] = useState<Template | null>(null);
  const [plan, setPlan] = useState<Change[] | null>(null);
  const [answers, setAnswers] = useState<Record<string, string>>({});
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);
  const [done, setDone] = useState(false);
  const [made, setMade] = useState<Var[]>([]);
  const base = `/api/environments/${project}/${env}/templates/`;
  const svcPath = (s: string) => `/api/services/${project}/${env}/${s}`;

  async function pick(id: string) {
    setErr(""); setPlan(null); setDone(false); setAnswers({});
    try {
      setT(await api<Template>("GET", "/api/templates/" + id));
      setPlan((await api<{ changes: Change[] }>("POST", base + id + "?dry=1")).changes);
    } catch (e) {
      setErr(errText(e));
    }
  }

  async function install() {
    if (!t) return;
    setBusy(true); setErr("");
    let step = "Installing the services";
    const created: string[] = [];
    const generated: Var[] = [];
    try {
      if (t.vars.some((v) => v.kind !== "ref")) {
        step = "Checking the keys";
        const k = await api<{ agent_pubkey: string; recovery_pubkey: string; server: string }>("GET", `/api/environments/${project}/${env}/keys`);
        if (!k.agent_pubkey) throw new Error(`Server ${k.server} has no secrets key; join it again.`);
        if (!k.recovery_pubkey) throw new Error("Make the recovery key first, in Settings.");
        step = "Installing the services";
      }
      await api("POST", base + t.id);
      created.push(...t.services.map((s) => s.name));
      for (const v of t.vars) {
        step = `Setting ${v.key} on ${v.service}`;
        if (v.kind === "ref") await setRef(svcPath(v.service), v.key, v.value ?? "");
        else if (v.kind === "generate") {
          const value = randomSecret();
          await setVar(svcPath(v.service), v.key, value, false);
          generated.push({ ...v, value });
        } else await setVar(svcPath(v.service), v.key, v.kind === "ask" ? answers[v.key] ?? "" : v.value ?? "", false);
      }
      const order = [...t.services.filter((s) => isDb(s.kind)), ...t.services.filter((s) => !isDb(s.kind))];
      for (const s of order) {
        step = `Deploying ${s.name}`;
        const d = await api<{ deploy_id: string }>("POST", svcPath(s.name) + "/deploys", { commit: "" });
        const until = Date.now() + deadline;
        for (;;) {
          const v = await api<{ state: string }>("GET", `/api/deploys/${d.deploy_id}`);
          if (v.state === "live") break;
          if (["failed", "rolled_back", "superseded"].includes(v.state)) throw new Error(`${s.name} did not start.`);
          if (Date.now() > until && !isDb(s.kind)) throw new Error(`${s.name} is still ${v.state}; follow it from its page.`);
          if (Date.now() > until) {
            const rest = order.filter((x) => !isDb(x.kind)).map((x) => x.name).join(", ");
            throw new Error(`${s.name} is still ${v.state}; deploy ${rest} from its page once ${s.name} is live.`);
          }
          await wait(poll);
        }
      }
      setMade(generated);
      setDone(true);
    } catch (e) {
      setErr(`${step} failed: ${errText(e)}` + (created.length ? ` Created: ${created.join(", ")}.` : ""));
    } finally {
      setBusy(false);
    }
  }

  const asks = t?.vars.filter((v) => v.kind === "ask") ?? [];
  return (
    <Shell crumb={[<Link to="/">Projects</Link>, <Link to={`/p/${project}/${env}`}>{project}</Link>, env, "Templates"]} active="/">
      <h1>Templates</h1>
      {!list.data ? <Loading what={list} /> : (
        <div className="grid3">
          {list.data.map((c) => (
            <button key={c.id} type="button" className={"card tpl" + (t?.id === c.id ? " on" : "")} disabled={busy} onClick={() => pick(c.id)}>
              <span className="row tpl-icons">{c.icons.map((svg, i) => <span key={i} aria-hidden="true" dangerouslySetInnerHTML={{ __html: svg }} />)}<b>{c.title}</b></span>
              <span className="muted" style={{ textAlign: "left" }}>{c.line}</span>
              <span className="dim" style={{ textAlign: "left" }}>{c.memory}</span>
            </button>
          ))}
        </div>
      )}
      {t && plan && !done && (
        <div className="card pad col" style={{ maxWidth: 720 }}>
          <h2>{t.title}</h2>
          {asks.map((v) => (
            <Field key={v.key} label={v.label ?? v.key}>{(id) => <input id={id} className="in" value={answers[v.key] ?? ""} onChange={(e) => setAnswers({ ...answers, [v.key]: e.target.value })} />}</Field>
          ))}
          <PlanBox changes={plan} />
          <div><button className="btn pri" type="button" disabled={busy} onClick={install}>{busy ? "Installing…" : "Install"}</button></div>
        </div>
      )}
      {t && done && (
        <div className="card pad col" style={{ maxWidth: 720 }}>
          <h2>{t.title}</h2>
          <p>{t.note}</p>
          {made.map((v) => (
            <div key={v.service + v.key} className="row"><span className="muted">{`${v.key} on ${v.service}`}</span><code style={{ wordBreak: "break-all" }}>{v.value}</code><Copy text={v.value ?? ""} /></div>
          ))}
          <div className="row">{t.services.map((s) => <Link key={s.name} to={`/p/${project}/${env}/s/${s.name}`}>{s.name}</Link>)}</div>
        </div>
      )}
      {err && <Callout tone="bad">{err}</Callout>}
    </Shell>
  );
}
