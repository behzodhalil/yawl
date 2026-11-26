import { useEffect, useState } from "react";
import { api } from "../api";
import { reseal } from "../reseal";
import { decodeRecovery, sealTo } from "../seal";
import { Shell } from "../Shell";
import sodium from "libsodium-wrappers";
import type { MovePlan, MoveView, ProjectView, ServerView } from "../types";
import { Button, LayerCard, Select, cn, inputVariants, linkVariants } from "@cloudflare/kumo";
import { Callout, EnvName, Field, Link, Loading, errText, useLoad, Card } from "../ui";

const UNDONE = ["stop", "backup", "switch_server"];

const STEPS: [string, string][] = [
  ["stop", "Stop the apps on the old server"],
  ["backup", "Back up the databases"],
  ["switch_server", "Switch the environment to the new server"],
  ["restore", "Restore the databases"],
  ["deploy", "Build and start the apps"],
  ["clean", "Clean up the old server"],
];

function KeyField({ value, onChange }: { value: string; onChange: (v: string) => void }) {
  return (
    <Field label="Recovery key" hint="Used in this browser to re-seal every secret to the new server. The server never sees it.">
      {(id) => <textarea id={id} className={cn(inputVariants({ focusIndicator: true }), "h-auto p-2 font-mono")} rows={2} autoComplete="off" spellCheck={false}
        value={value} onChange={(e) => onChange(e.target.value)} />}
    </Field>
  );
}

function Progress({ project, env, move, onChange }: { project: string; env: string; move: MoveView; onChange: () => void }) {
  const [key, setKey] = useState("");
  const [err, setErr] = useState("");
  const at = STEPS.findIndex(([s]) => s === move.step);
  async function retry() {
    setErr("");
    try {
      const k = await api<{ agent_pubkey: string; recovery_pubkey: string }>("GET", `/api/environments/${project}/${env}/keys`);
      const text = key.trim();
      let recovery_box = "";
      if (text) {
        const { pub } = await decodeRecovery(text);
        if (sodium.to_base64(pub, sodium.base64_variants.ORIGINAL) !== k.recovery_pubkey) throw new Error("This is not this panel's recovery key.");
        recovery_box = await sealTo(k.agent_pubkey, new TextEncoder().encode(text));
      }
      setKey("");
      await api("POST", `/api/moves/${move.id}/retry`, { recovery_box });
      onChange();
    } catch (e) {
      setErr(errText(e));
    }
  }
  return (
    <Card title={move.state === "done" ? `${env} moved to ${move.to}.` : `Moving ${env} to ${move.to}.`} level={3}>
      <ol className="m-0 flex list-decimal flex-col gap-1 pl-5">
        {STEPS.map(([s, label], i) => (
          <li key={s} className={i < at || move.state === "done" ? "text-kumo-subtle" : undefined}>
            {label}{i === at && move.state === "running" ? " (now)" : ""}
          </li>
        ))}
      </ol>
      {move.state === "failed" && (
        <>
          <Callout tone="bad">{move.cause}</Callout>
          <KeyField value={key} onChange={setKey} />
          <Button variant="primary" className="self-start" onClick={retry}>Retry</Button>
        </>
      )}
      {err && <Callout tone="bad">{err}</Callout>}
    </Card>
  );
}

export function Move({ project, env }: { project: string; env: string }) {
  const servers = useLoad<ServerView[]>("/api/servers");
  const tree = useLoad<ProjectView[]>("/api/projects");
  const latest = useLoad<MoveView>(`/api/environments/${project}/${env}/moves/latest`);
  const [to, setTo] = useState("");
  const [plan, setPlan] = useState<MovePlan | null>(null);
  const [planErr, setPlanErr] = useState("");
  const [key, setKey] = useState("");
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);
  const current = tree.data?.find((p) => p.name === project)?.envs.find((e) => e.name === env)?.server;
  const undone = latest.data?.state === "failed" && UNDONE.includes(latest.data.step) ? latest.data : null;
  const move = latest.data && latest.data.state !== "done" && !undone ? latest.data : null;

  useEffect(() => {
    if (latest.data?.state !== "running") return;
    const t = setTimeout(latest.reload, 2000);
    return () => clearTimeout(t);
  }, [latest.data, latest.reload]);

  useEffect(() => {
    setPlan(null);
    setPlanErr("");
    if (!to) return;
    api<MovePlan>("GET", `/api/environments/${project}/${env}/move?to=${encodeURIComponent(to)}`).then(setPlan, (e) => setPlanErr(errText(e)));
  }, [project, env, to]);

  async function start() {
    if (!plan) return;
    setBusy(true);
    setErr("");
    try {
      const sealed = await reseal(plan, key);
      setKey("");
      await api("POST", `/api/environments/${project}/${env}/move`, { to, ...sealed });
      latest.reload();
    } catch (e) {
      setErr(errText(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Shell crumb={[<Link to="/">Projects</Link>, <Link to={`/p/${project}/${env}`}>{project}</Link>, <EnvName env={env} />, "Move"]} active="/">
      <div className="flex flex-wrap items-end justify-between gap-4"><div><h1>Move {env}</h1>{current && <p className="mt-1 text-kumo-subtle">{env} runs on {current}. Every service moves together.</p>}</div></div>
      {!servers.data || !tree.data ? <Loading what={servers.data ? tree : servers} /> : move ? (
        <Progress project={project} env={env} move={move} onChange={latest.reload} />
      ) : servers.data.every((s) => s.name === current) ? (
        <Callout tone="warn" title="There is no other server to move to.">
          <Link to="/servers/new" className={linkVariants({ variant: "inline" })}>Add a server</Link>
        </Callout>
      ) : (
        <LayerCard className="flex flex-col gap-3 p-4">
          {latest.data?.state === "done" && <Callout tone="ok">{`${env} moved to ${latest.data.to}.`}</Callout>}
          {latest.data?.state === "done" && !latest.data.cleaned && (latest.data.cause
            ? <Callout tone="warn">{latest.data.cause}</Callout>
            : <p className="text-kumo-subtle">{`${latest.data.from} is cleaned up once it is back online.`}</p>)}
          {undone && <Callout tone="warn">{`The last move was undone: ${undone.cause}.`}</Callout>}
          <Field label="Move to">
            {(id) => (
              <Select aria-labelledby={id + "-label"} value={to || null} placeholder="Pick a server" onValueChange={(v) => setTo(String(v ?? ""))}
                items={Object.fromEntries(servers.data!.filter((s) => s.name !== current).map((s) => [s.name, s.name]))} />
            )}
          </Field>
          {planErr && <Callout tone="bad">{planErr}</Callout>}
          {plan && <ol className="m-0 flex list-decimal flex-col gap-1 pl-5">{plan.lines.map((l) => <li key={l}><code>{l}</code></li>)}</ol>}
          <KeyField value={key} onChange={setKey} />
          {err && <Callout tone="bad">{err}</Callout>}
          <Button variant="primary" className="self-start" disabled={!plan || !key.trim() || busy} onClick={start}>{`Move ${env} to ${to || "…"}`}</Button>
        </LayerCard>
      )}
    </Shell>
  );
}
