import { useEffect, useRef, useState, type ReactNode } from "react";
import { reveal } from "../../seal";
import type { ServiceForm, ServiceView } from "../../types";
import { startDeploy } from "../../serviceActions";
import { navigate } from "../../router";
import { Button, ClipboardText, InlineCopyText, Switch, TagInput, linkVariants } from "@cloudflare/kumo";
import { Callout, Field, Link, Loading, errText, useLoad, useUnsaved, Input, Card } from "../../ui";
import { ago, dateTime, urlParts } from "../../format";
import { PlanBox } from "../NewService";
import { useFormSave } from "./Settings";

const CIDR = /^[0-9a-fA-F:.]+(\/\d{1,3})?$/;

const SECRET_LABEL: Record<string, string> = { URL: "URL", OUTSIDE_URL: "Outside URL" };

function Pair({ k, children }: { k: string; children: ReactNode }) {
  return <div className="contents"><dt className="text-kumo-subtle">{k}</dt><dd className="m-0 flex min-w-0 flex-wrap items-center gap-2.5">{children}</dd></div>;
}

export const REF_KEY: Record<string, string> = { postgres: "DATABASE_URL", valkey: "REDIS_URL", mysql: "DATABASE_URL", mongodb: "MONGODB_URI", mariadb: "DATABASE_URL", clickhouse: "CLICKHOUSE_URL" };

export function DbInfo({ project, env, service }: { project: string; env: string; service: string }) {
  const svcPath = `/api/services/${project}/${env}/${service}`;
  const view = useLoad<ServiceView>(svcPath);
  const [form, setFormRaw] = useState<ServiceForm | null>(null);
  const [open, setOpen] = useState(false);
  const [allow, setAllow] = useState("");
  const [local, setLocal] = useState("");
  const [shown, setShown] = useState<Record<string, string>>({});
  const hideTimers = useRef(new Map<string, number>());
  useEffect(() => () => hideTimers.current.forEach(clearTimeout), []);
  const [pitrFromWarning, setPitrFromWarning] = useState(false);
  const backups = useLoad<{ state: string; created_at: number }[]>(`${svcPath}/backups`);
  const save = useFormSave(project, env, () => view.reload());
  const savePitr = useFormSave(project, env, async () => {
    try {
      navigate(`/p/${project}/${env}/s/${service}/deploys/${await startDeploy(svcPath)}`);
    } catch (e) { setLocal(errText(e)); }
    view.reload();
  });
  useEffect(() => {
    if (!view.data) return;
    setFormRaw(view.data.form);
    setOpen(view.data.form.outside_port > 0);
    setAllow(view.data.form.allow.join("\n"));
  }, [view.data]);
  const was = view.data?.form;
  const dirty = !!was && !!form && (open !== was.outside_port > 0 || allow !== was.allow.join("\n") || (open && form.outside_port !== was.outside_port));
  useUnsaved(dirty);
  if (!view.data || !form) return <Loading what={view} />;
  const setForm = (f: ServiceForm) => (setFormRaw(f), save.reset());

  const wanted = (): ServiceForm => open
    ? { ...form, allow: allow.split("\n").map((l) => l.trim()).filter(Boolean) }
    : { ...form, outside_port: 0, allow: [] };

  function plan() {
    const f = wanted();
    if (open && (!f.outside_port || f.allow.length === 0)) return setLocal("Give a port and at least one address.");
    setLocal("");
    save.show(f);
  }

  const hide = (key: string) => {
    clearTimeout(hideTimers.current.get(key));
    hideTimers.current.delete(key);
    setShown((s) => { const n = { ...s }; delete n[key]; return n; });
  };

  async function show(key: string) {
    try {
      const v = await reveal(svcPath, key);
      setShown((s) => ({ ...s, [key]: v }));
      clearTimeout(hideTimers.current.get(key));
      hideTimers.current.set(key, window.setTimeout(() => hide(key), 30_000));
    } catch (e) {
      setLocal(errText(e));
    }
  }

  const lastBackup = (backups.data ?? []).filter((b) => b.state === "ok").reduce<number | undefined>((m, b) => (m === undefined || b.created_at > m ? b.created_at : m), undefined);
  const unprotected = env === "production" && form.kind === "postgres" && !form.pitr;
  const startPitr = (fromWarning: boolean) => (setPitrFromWarning(fromWarning), savePitr.show({ ...form, pitr: !form.pitr }));
  const pitrStep = (fromWarning: boolean, start: string) => {
    const here = pitrFromWarning === fromWarning;
    return (
      <>
        {here && savePitr.plan && <PlanBox changes={savePitr.plan} />}
        {here && savePitr.err && <Callout tone="bad">{savePitr.err}</Callout>}
        {!savePitr.plan
          ? <div className="flex flex-wrap items-center gap-2.5"><Button onClick={() => startPitr(fromWarning)}>{start}</Button></div>
          : here && <div className="flex flex-wrap items-center gap-2.5"><Button variant="primary" onClick={() => savePitr.apply({ ...form, pitr: !form.pitr })}>Apply (restarts the database)</Button></div>}
      </>
    );
  };

  return (
    <div className="flex flex-col gap-3">
      {unprotected && (
        <Callout tone="warn">
          <div className="flex flex-col gap-3">
            <span>Production database without point-in-time recovery. A restore can only reach the last nightly backup.</span>
            {pitrStep(true, "Turn on point-in-time recovery")}
          </div>
        </Callout>
      )}
      <Card title="Connect" className="max-w-[var(--w-form)]">
        <dl className="m-0 grid grid-cols-[max-content_minmax(0,1fr)] items-center gap-x-6 gap-y-3">
          <Pair k="Reference"><InlineCopyText>{`${service}.url`}</InlineCopyText></Pair>
          <Pair k="Use as"><span className="font-mono text-sm">{`${REF_KEY[form.kind] ?? "DATABASE_URL"} → ${service}.url`}</span></Pair>
          {lastBackup !== undefined && <Pair k="Last backup"><span title={ago(lastBackup)}>{dateTime(lastBackup)}</span></Pair>}
          {(["URL", ...(form.outside_port ? ["OUTSIDE_URL"] : [])] as string[]).map((k) => [
            <Pair key={k} k={SECRET_LABEL[k]}>
              {shown[k] !== undefined ? <ClipboardText size="base" text={shown[k]} className="min-w-0 flex-1" /> : <span className="font-mono">••••••••</span>}
              {shown[k] !== undefined
                ? <Button size="sm" aria-label={`Hide ${SECRET_LABEL[k]}`} onClick={() => hide(k)}>Hide</Button>
                : <Button size="sm" aria-label={`Show ${SECRET_LABEL[k]}`} onClick={() => show(k)}>Show</Button>}
            </Pair>,
            ...urlParts(shown[k] ?? "").map(([part, v]) => (
              <Pair key={k + part} k={k === "URL" ? part : `Outside ${part.toLowerCase()}`}><InlineCopyText>{v}</InlineCopyText></Pair>
            )),
          ])}
        </dl>
      </Card>
      <Card title="Used by" className="max-w-[var(--w-form)]">
        {view.data.used_by?.length ? <div className="flex flex-col items-start gap-1">{view.data.used_by.map((u) => (
          <Link key={u.env + "/" + u.service} to={`/p/${project}/${u.env}/s/${u.service}`} className={linkVariants({ variant: "inline" }) + " font-mono"}>{u.env === env ? u.service : `${u.env}/${u.service}`}</Link>
        ))}</div> : <p className="text-kumo-subtle">{`No service references ${service}.url yet.`}</p>}
      </Card>
{form.kind === "postgres" && (
      <Card title="Outside access" className="max-w-[var(--w-form)]">
        <p className="text-kumo-subtle">Off by default. On, Postgres listens on a public port over TLS, only for the addresses listed.</p>
        <Switch label="Allow connections from outside" checked={open} onClick={() => (setOpen(!open), save.reset())} />
        {open && (
          <>
            <Field label="Outside port" hint="1024 to 65535, once per server.">{(id) => (
              <Input id={id} className="w-full font-mono" type="number" min={1024} max={65535} value={form.outside_port || ""} onChange={(e) => setForm({ ...form, outside_port: Number(e.target.value) })} />
            )}</Field>
            <TagInput label="Allowed addresses" description="One address or range each, like 203.0.113.4/32." placeholder="203.0.113.4/32" className="font-mono"
              value={allow ? allow.split("\n") : []} validateValue={(v: string) => CIDR.test(v.trim())}
              onValueChange={(v: string[]) => (setAllow(v.map((x) => x.trim()).join("\n")), save.reset())} />
          </>
        )}
        {local && <Callout tone="warn">{local}</Callout>}
        {save.plan && <PlanBox changes={save.plan} />}
        {save.err && <Callout tone="bad">{save.err}</Callout>}
        <div className="flex flex-wrap items-center gap-3 border-t border-kumo-hairline pt-4">
          {!save.plan ? <Button variant={open ? "primary" : "secondary"} disabled={!dirty} onClick={plan}>Show the plan</Button>
            : <Button variant="primary" onClick={() => save.apply(wanted())}>Apply</Button>}
          {dirty && <span className="text-kumo-subtle">Unsaved changes</span>}
        </div>
      </Card>
      )}
      {form.kind === "postgres" && (
      <Card title="Point in time recovery" className="max-w-[var(--w-form)]">
        <p className="text-kumo-subtle">{form.pitr ? "On: 7 days of changes, restorable to any second, from Backups." : "Off: restores reach the nightly backups."}</p>
        {pitrStep(false, form.pitr ? "Stop keeping changes (restarts the database)" : "Keep 7 days of changes (restarts the database)")}
      </Card>
      )}
    </div>
  );
}
