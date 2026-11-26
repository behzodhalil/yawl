import { isDb } from "../serviceForm";
import { useState } from "react";
import { Badge, Button, ClipboardText, Grid, InputGroup, LayerCard, Loader, Tabs, cn, linkVariants } from "@cloudflare/kumo";
import { api } from "../api";
import { spaced } from "../format";
import { randomSecret, setRef, setVar } from "../seal";
import { Shell } from "../Shell";
import type { ProjectView, ServiceForm, Volume } from "../types";
import { Callout, Empty, EnvName, Field, Icon, Link, Loading, errText, useLoad, useReveal, Input, Card } from "../ui";

type Card = { id: string; title: string; line: string; category: string; new?: boolean; memory: string; icons: string[]; services: { name: string; kind: string; volumes: Volume[] }[] };
type Var = { service: string; key: string; kind: "fixed" | "generate" | "ask" | "ref"; value?: string; label?: string };
type Template = Omit<Card, "services"> & { note: string; services: ServiceForm[]; vars: Var[] };

const wait = (ms: number) => new Promise((r) => setTimeout(r, ms));

const CATEGORIES: [string, string][] = [["backend", "Backend"], ["content", "Content"], ["analytics", "Analytics"], ["automation", "Automation"], ["tools", "Tools"]];
const MAX_LISTED = 4;

function servicesLine(c: Card) {
  if (c.services.length === 1) {
    const [s] = c.services;
    return <><code>{s.name}</code> ({[s.kind, ...s.volumes.map((v) => `${v.name} volume`)].join(", ")})</>;
  }
  const shown = c.services.slice(0, MAX_LISTED);
  return <>{shown.map((s, i) => <span key={s.name}>{i > 0 && ", "}<code>{s.name}</code></span>)}{c.services.length > MAX_LISTED && "…"}</>;
}

function serviceDetail(s: ServiceForm) {
  return [s.kind, s.kind === "web" && s.port ? `port ${s.port}` : "", spaced(s.memory)].filter(Boolean).join(" · ");
}

function WillBeCreated({ t }: { t: Template }) {
  const volumes = t.services.flatMap((s) => (s.volumes ?? []).map((v) => ({ ...v, service: s.name })));
  const row = (icon: string, name: string, detail: string, tag: string) => (
    <li key={tag + name} className="flex items-center gap-3 py-2">
      <span className="text-kumo-subtle"><Icon name={icon} /></span>
      <span className="min-w-0 flex-1"><span className="block font-mono text-sm">{name}</span><span className="block text-sm text-kumo-subtle">{detail}</span></span>
      <Badge variant="outline">{tag}</Badge>
    </li>
  );
  return (
    <Card title="Will be created">
      <ul aria-label="Will be created" className="flex flex-col divide-y divide-kumo-hairline">
        {t.services.map((s) => row(isDb(s.kind) ? "db" : s.kind === "web" ? "globe" : s.kind === "cron" ? "cron" : "worker", s.name, serviceDetail(s), isDb(s.kind) ? "database" : s.kind))}
        {volumes.map((v) => row("server", `${v.service}/${v.name}`, `volume at ${v.path}`, "volume"))}
      </ul>
      {(volumes.length > 0 || t.services.some((s) => isDb(s.kind))) && <p className="text-sm text-kumo-subtle">Databases and volumes are backed up encrypted every night.</p>}
    </Card>
  );
}

export function Templates({ project, env, poll = 2000, deadline = 10 * 60_000 }: { project: string; env: string; poll?: number; deadline?: number }) {
  const list = useLoad<Card[]>("/api/templates");
  const tree = useLoad<ProjectView[]>("/api/projects");
  const server = tree.data?.find((p) => p.name === project)?.envs.find((e) => e.name === env)?.server;
  const [category, setCategory] = useState("all");
  const [q, setQ] = useState("");
  const [domain, setDomain] = useState("");
  const [t, setT] = useState<Template | null>(null);
  const [plan, setPlan] = useState<unknown[] | null>(null);
  const [answers, setAnswers] = useState<Record<string, string>>({});
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);
  const [done, setDone] = useState(false);
  const [made, setMade] = useState<Var[]>([]);
  const reveal = useReveal<HTMLDivElement>(t && plan && !done ? t.id : null);
  const base = `/api/environments/${project}/${env}/templates/`;
  const svcPath = (s: string) => `/api/services/${project}/${env}/${s}`;

  async function pick(id: string) {
    setErr(""); setPlan(null); setDone(false); setAnswers({}); setDomain("");
    try {
      setT(await api<Template>("GET", "/api/templates/" + id));
      setPlan((await api<{ changes: unknown[] }>("POST", base + id + "?dry=1")).changes);
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
      await api("POST", base + t.id, domain.trim() ? { domain: domain.trim() } : undefined);
      created.push(...t.services.map((s) => s.name));
      for (const v of t.vars) {
        step = `Setting ${v.key} on ${v.service}`;
        if (v.kind === "ref") await setRef(svcPath(v.service), v.key, v.value ?? "");
        else if (v.kind === "generate") {
          const value = randomSecret();
          await setVar(svcPath(v.service), v.key, value, false);
          generated.push({ ...v, value });
        } else await setVar(svcPath(v.service), v.key, v.kind === "ask" ? (answers[v.key] ?? "").trim() : v.value ?? "", false);
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
  const unanswered = asks.some((v) => !(answers[v.key] ?? "").trim());
  const generated = t?.vars.filter((v) => v.kind === "generate").map((v) => v.key) ?? [];
  const words = q.trim().toLowerCase();
  const found = list.data?.filter((c) => !words || [c.title, c.line, ...c.services.map((s) => s.name)].some((x) => x.toLowerCase().includes(words)));
  const count = (c: string) => found?.filter((x) => x.category === c).length ?? 0;
  const tabs = [{ value: "all", label: `All · ${found?.length ?? 0}` }, ...CATEGORIES.filter(([c]) => list.data?.some((x) => x.category === c)).map(([c, label]) => ({ value: c, label: `${label} · ${count(c)}` }))];
  const shown = found?.filter((c) => category === "all" || c.category === category);
  return (
    <Shell crumb={[<Link to="/">Projects</Link>, <Link to={`/p/${project}/${env}`}>{project}</Link>, <EnvName env={env} />, "Templates"]} active="/">
      <div><h1>Templates</h1><p className="mt-1 text-kumo-subtle">Ready stacks that deploy into a project on your own server.</p></div>
      {!shown ? <Loading what={list} /> : (
        <>
          <div className="flex flex-wrap items-center justify-between gap-3">
            <Tabs variant="segmented" aria-label="Template kinds" tabs={tabs} value={category} onValueChange={setCategory} />
            <InputGroup className="w-full sm:w-60">
              <InputGroup.Addon><Icon name="search" /></InputGroup.Addon>
              <InputGroup.Input type="search" aria-label="Search templates" placeholder="Search" value={q} onChange={(e) => setQ(e.target.value)} />
            </InputGroup>
          </div>
          {shown.length === 0 && <Empty text="No template matches the search." />}
          <Grid variant="3up" gap="sm">
            {shown.map((c) => (
              <LayerCard key={c.id} render={<button type="button" disabled={busy} onClick={() => pick(c.id)} />}
                className={cn("flex cursor-pointer flex-col gap-2 p-4 text-left hover:ring-kumo-line", t?.id === c.id && "bg-kumo-tint ring-2 ring-kumo-brand")}>
                <span className="flex flex-wrap items-center gap-1.5">
                  {c.icons.map((svg, i) => <span key={i} aria-hidden="true" className="inline-flex size-7 [&_svg]:size-full" dangerouslySetInnerHTML={{ __html: svg }} />)}
                  <b className="font-semibold">{c.title}</b>
                  {c.new && <Badge variant="info">New</Badge>}
                </span>
                <span className="text-kumo-subtle">{c.line}</span>
                <dl className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 text-sm">
                  <dt className="text-kumo-subtle">Services</dt><dd className="m-0">{servicesLine(c)}</dd>
                  <dt className="text-kumo-subtle">Memory</dt><dd className="m-0">{spaced(c.memory)}</dd>
                </dl>
              </LayerCard>
            ))}
          </Grid>
        </>
      )}
      {t && plan && !done && (
        <div ref={reveal} className="grid items-start gap-4 lg:grid-cols-[minmax(0,1fr)_minmax(0,1fr)]">
          <Card title={`Deploy ${t.title}`}>
            <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1">
              <dt className="text-kumo-subtle">Project</dt><dd className="m-0">{project}</dd>
              <dt className="text-kumo-subtle">Environment</dt><dd className="m-0">{env}</dd>
              {server && <><dt className="text-kumo-subtle">Server</dt><dd className="m-0">{server}</dd></>}
            </dl>
            {t.services.some((s) => s.kind === "web") && (
              <Field label="Domain" hint="HTTPS comes with it. Leave it empty for a sslip.io address.">{(id) => <Input id={id} value={domain} placeholder="app.example.com" onChange={(e) => setDomain(e.target.value)} />}</Field>
            )}
            {asks.map((v) => (
              <Field key={v.key} label={v.label ?? v.key} hint="Required.">{(id) => <Input id={id} required value={answers[v.key] ?? ""} onChange={(e) => setAnswers({ ...answers, [v.key]: e.target.value })} />}</Field>
            ))}
            {generated.length > 0 && <p className="text-sm text-kumo-subtle">{`${generated.join(", ")} ${generated.length > 1 ? "are" : "is"} generated and shown once, then kept as a secret variable.`}</p>}
            <div className="flex flex-wrap items-center justify-end gap-2.5 border-t border-kumo-hairline pt-4">
              {busy && <Loader size="sm" />}
              <Button variant="secondary" disabled={busy} onClick={() => { setT(null); setPlan(null); }}>Cancel</Button>
              <Button variant="primary" disabled={busy || unanswered} onClick={install}>{busy ? "Deploying…" : "Deploy template"}</Button>
            </div>
          </Card>
          <WillBeCreated t={t} />
        </div>
      )}
      {t && done && (
        <Card title={t.title} className="max-w-[var(--w-form)]">
          <p>{t.note}</p>
          {made.map((v) => (
            <div key={v.service + v.key} className="flex flex-wrap items-center gap-2.5"><span className="text-kumo-subtle">{`${v.key} on ${v.service}`}</span><ClipboardText className="min-w-0 max-w-full" text={v.value ?? ""} labels={{ copyAction: `Copy ${v.key}` }} /></div>
          ))}
          <div className="flex flex-wrap items-center gap-2.5">{t.services.map((s) => <Link key={s.name} to={`/p/${project}/${env}/s/${s.name}`} className={linkVariants({ variant: "inline" })}>{s.name}</Link>)}</div>
        </Card>
      )}
      {err && <Callout tone="bad">{err}</Callout>}
    </Shell>
  );
}
