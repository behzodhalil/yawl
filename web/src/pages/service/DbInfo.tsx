import { useEffect, useState } from "react";
import { api } from "../../api";
import { reveal } from "../../seal";
import type { ServiceForm, ServiceView } from "../../types";
import { navigate } from "../../router";
import { Callout, Copy, Field, Link, Loading, errText, useLoad, useUnsaved } from "../../ui";
import { ago, dateTime } from "../../format";
import { PlanBox } from "../NewService";
import { useFormSave } from "./Settings";

export const REF_KEY: Record<string, string> = { postgres: "DATABASE_URL", valkey: "REDIS_URL", mysql: "DATABASE_URL", mongodb: "MONGODB_URI", mariadb: "DATABASE_URL", clickhouse: "CLICKHOUSE_URL" };

export function DbInfo({ project, env, service }: { project: string; env: string; service: string }) {
  const svcPath = `/api/services/${project}/${env}/${service}`;
  const view = useLoad<ServiceView>(svcPath);
  const [form, setFormRaw] = useState<ServiceForm | null>(null);
  const [open, setOpen] = useState(false);
  const [allow, setAllow] = useState("");
  const [local, setLocal] = useState("");
  const [shown, setShown] = useState<Record<string, string>>({});
  const [pitrFromWarning, setPitrFromWarning] = useState(false);
  const backups = useLoad<{ state: string; created_at: number }[]>(`${svcPath}/backups`);
  const save = useFormSave(project, env, () => view.reload());
  const savePitr = useFormSave(project, env, async () => {
    try {
      const d = await api<{ deploy_id: string }>("POST", svcPath + "/deploys", { commit: "" });
      navigate(`/p/${project}/${env}/s/${service}/deploys/${d.deploy_id}`);
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

  const hide = (key: string) => setShown((s) => { const n = { ...s }; delete n[key]; return n; });

  async function show(key: string) {
    try {
      const v = await reveal(svcPath, key);
      setShown((s) => ({ ...s, [key]: v }));
      setTimeout(() => hide(key), 30_000);
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
          ? <div className="row"><button className="btn" onClick={() => startPitr(fromWarning)}>{start}</button></div>
          : here && <div className="row"><button className="btn pri" onClick={() => savePitr.apply({ ...form, pitr: !form.pitr })}>Apply (restarts the database)</button></div>}
      </>
    );
  };

  return (
    <div className="col">
      {unprotected && (
        <Callout tone="warn">
          <div className="col">
            <span>Production database without point-in-time recovery. A restore can only reach the last nightly backup.</span>
            {pitrStep(true, "Turn on point-in-time recovery")}
          </div>
        </Callout>
      )}
      <div className="card pad col" style={{ maxWidth: 720 }}>
        <h2>Connect</h2>
        <div className="row"><span className="muted">Internal reference</span><code>{`${service}.url`}</code><Copy text={`${service}.url`} /></div>
        <p className="muted">Use it as a reference: {REF_KEY[form.kind] ?? "DATABASE_URL"} → {`${service}.url`}</p>
        {lastBackup !== undefined && <div className="row"><span className="muted">Last backup</span><span title={ago(lastBackup)}>{dateTime(lastBackup)}</span></div>}
        {(["URL", ...(form.outside_port ? ["OUTSIDE_URL"] : [])] as string[]).map((k) => (
          <div key={k} className="row">
            <code style={{ minWidth: 110 }}>{k}</code>
            <span className="mono" style={{ wordBreak: "break-all" }}>{shown[k] ?? "••••••••"}</span>
            {shown[k] !== undefined
              ? <button className="btn sm" aria-label={`Hide ${k}`} onClick={() => hide(k)}>Hide</button>
              : <button className="btn sm" aria-label={`Show ${k}`} onClick={() => show(k)}>Show</button>}
          </div>
        ))}
      </div>
      <div className="card pad col" style={{ maxWidth: 720 }}>
        <h2>Used by</h2>
        {view.data.used_by?.length ? view.data.used_by.map((u) => (
          <Link key={u.env + "/" + u.service} to={`/p/${project}/${u.env}/s/${u.service}`} className="l mono">{u.env === env ? u.service : `${u.env}/${u.service}`}</Link>
        )) : <p className="muted">{`No service references ${service}.url yet.`}</p>}
      </div>
{form.kind === "postgres" && (
      <div className="card pad col" style={{ maxWidth: 720 }}>
        <h2>Outside access</h2>
        <p className="muted">Off by default. On, Postgres listens on a public port over TLS, only for the addresses listed.</p>
        <label className="row" style={{ gap: 6 }}>
          <input type="checkbox" aria-label="Allow connections from outside" checked={open} onChange={(e) => (setOpen(e.target.checked), save.reset())} />
          Allow connections from outside
        </label>
        {open && (
          <>
            <Field label="Outside port" hint="1024 to 65535, once per server.">{(id) => (
              <input id={id} className="in mono" type="number" min={1024} max={65535} value={form.outside_port || ""} onChange={(e) => setForm({ ...form, outside_port: Number(e.target.value) })} />
            )}</Field>
            <Field label="Allowed addresses" hint="One CIDR per line, like 203.0.113.4/32.">{(id) => (
              <textarea id={id} className="in mono" rows={3} style={{ height: "auto", padding: 8 }} value={allow} onChange={(e) => (setAllow(e.target.value), save.reset())} />
            )}</Field>
          </>
        )}
        {local && <Callout tone="warn">{local}</Callout>}
        {save.plan && <PlanBox changes={save.plan} />}
        {save.err && <Callout tone="bad">{save.err}</Callout>}
        <div className="row">
          {!save.plan ? <button className={open ? "btn pri" : "btn"} disabled={!dirty} onClick={plan}>Show the plan</button>
            : <button className="btn pri" onClick={() => save.apply(wanted())}>Apply</button>}
          {dirty && <span className="muted">Unsaved changes</span>}
        </div>
      </div>
      )}
      {form.kind === "postgres" && (
      <div className="card pad col" style={{ maxWidth: 720 }}>
        <h2>Point in time recovery</h2>
        <p className="muted">{form.pitr ? "On: 7 days of changes, restorable to any second, from Backups." : "Off: restores reach the nightly backups."}</p>
        {pitrStep(false, form.pitr ? "Stop keeping changes (restarts the database)" : "Keep 7 days of changes (restarts the database)")}
      </div>
      )}
    </div>
  );
}
