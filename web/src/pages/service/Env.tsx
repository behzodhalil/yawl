import { Badge, Button, InputGroup, LayerCard, LayerDialog, Select, SensitiveInput, Switch, Table, linkVariants } from "@cloudflare/kumo";
import { isDb } from "../../serviceForm";
import { useContext, useEffect, useRef, useState, type FormEvent } from "react";
import { api } from "../../api";
import { randomKey, reveal, sealTo, setRef, setVar } from "../../seal";
import { startDeploy } from "../../serviceActions";
import { navigate } from "../../router";
import { Deploys } from "./Deploy";
import type { DiffRow, ProjectView, ServiceView } from "../../types";
import { ButtonLink, Callout, Confirm, Field, Icon, Link, Loading, Menu, MenuItem, Modal, errText, useBusy, useLoad, Input, Card } from "../../ui";
import { stateKind } from "../../format";
import { Sources } from "./Sources";

type Entry = { key: string; scope: "service" | "shared" | "team" | "server"; service?: string; build?: boolean; ref?: string; updated_at: number };

function keySet(es: Entry[]) {
  return new Set(es.map((e) => (e.scope === "shared" ? e.key : `${e.service}/${e.key}`)));
}

const STATE: Record<DiffRow["state"], (env: string, other: string) => string> = {
  same: () => "same", different: () => "different", only_left: (e) => `only in ${e}`, only_right: (_, o) => `only in ${o}`,
};

function Diff({ project, env, other, service }: { project: string; env: string; other: string; service: string }) {
  const a = useLoad<Entry[]>(`/api/environments/${project}/${env}/env`);
  const b = useLoad<Entry[]>(`/api/environments/${project}/${other}/env`);
  const [rows, setRows] = useState<DiffRow[] | null>(null);
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);
  if (!a.data || !b.data) return null;
  const A = keySet(a.data), B = keySet(b.data);
  const lines = [...[...A].filter((k) => !B.has(k)).map((k) => `only in ${env}: ${k}`), ...[...B].filter((k) => !A.has(k)).map((k) => `only in ${other}: ${k}`)];
  async function compare() {
    setBusy(true);
    setErr("");
    try {
      const k = await randomKey();
      const keys: Record<string, string> = {};
      for (const e of [env, other]) {
        const { agent_pubkey } = await api<{ agent_pubkey: string }>("GET", `/api/environments/${project}/${e}/keys`);
        keys[e] = await sealTo(agent_pubkey, k);
      }
      const all = await api<DiffRow[]>("POST", `/api/projects/${project}/compare`, { left: env, right: other, keys });
      setRows(all.filter((r) => r.service === "" || r.service === service));
    } catch (e) {
      setErr(errText(e));
    } finally {
      setBusy(false);
    }
  }
  return (
    <Card title={<>Compared with {other}</>} level={3}>
      <div className="flex flex-col gap-1">
        {rows ? null : lines.length === 0 ? <span className="text-kumo-subtle">Both have the same keys.</span>
          : lines.map((l) => <code key={l}>{l}</code>)}
        {rows && rows.map((r) => <code key={r.service + "/" + r.key}>{`${r.key}: ${STATE[r.state](env, other)}`}</code>)}
      </div>
      {!rows && <div><Button disabled={busy} onClick={compare}>Compare values</Button></div>}
      {err && <Callout tone="bad">{err}</Callout>}
    </Card>
  );
}

async function writeClipboard(value: Promise<string>) {
  if (!("ClipboardItem" in window && navigator.clipboard?.write)) return navigator.clipboard.writeText(await value);
  const blob = value.then((v) => new Blob([v], { type: "text/plain" }));
  blob.catch(() => {});
  return navigator.clipboard.write([new ClipboardItem({ "text/plain": blob })]);
}

const GIVEN = new Set(["team", "server"]);

function Provided({ port, server, given, own }: { port?: number; server?: string; given: Entry[]; own: Entry[] }) {
  const rows: [string, string, string][] = [
    ...(port ? [["PORT", String(port), "Yawl"] as [string, string, string]] : []),
    ...given.map((v): [string, string, string] => [v.key, "••••••••", v.scope === "team" ? "Shared · team" : `Shared · server${server ? " " + server : ""}`]),
  ];
  if (rows.length === 0) return null;
  const mine = new Set(own.map((v) => v.key));
  return (
    <Card title="Provided by Yawl" level={3} actions={given.length > 0 && <ButtonLink to="/shared" variant="ghost" size="sm">Shared variables</ButtonLink>}>
      <p className="text-kumo-subtle">The app gets these without setting them. A variable set on this service wins.</p>
      <Table>
        <Table.Header><Table.Row><Table.Head>Key</Table.Head><Table.Head>Value</Table.Head><Table.Head>From</Table.Head></Table.Row></Table.Header>
        <Table.Body>
          {rows.map(([k, value, from]) => (
            <Table.Row key={k + from}>
              <Table.Cell><span className="inline-flex flex-wrap items-center gap-1.5"><code>{k}</code>{mine.has(k) && <Badge variant="outline">overridden</Badge>}</span></Table.Cell>
              <Table.Cell className="font-mono text-sm">{value}</Table.Cell>
              <Table.Cell className="text-kumo-subtle">{from}</Table.Cell>
            </Table.Row>
          ))}
        </Table.Body>
      </Table>
    </Card>
  );
}

export function Env({ project, env, service }: { project: string; env: string; service: string }) {
  const svcPath = `/api/services/${project}/${env}/${service}`;
  const envPath = `/api/environments/${project}/${env}`;
  const list = useLoad<Entry[]>(svcPath + "/env");
  const deploys = useContext(Deploys);
  useEffect(() => deploys.reload(), [deploys.reload]);
  const liveAt = deploys.data?.find((d) => d.live)?.created_at;
  const changed = (v: Entry) => liveAt !== undefined && v.updated_at > liveAt;
  const changes = list.data?.filter(changed).length ?? 0;
  const running = liveAt === undefined ? undefined : deploys.data?.find((d) => d.created_at > liveAt && stateKind(d.state) === "busy");
  const view = useLoad<ServiceView>(svcPath);
  const tree = useLoad<ProjectView[]>("/api/projects");
  const p = tree.data?.find((x) => x.name === project);
  const dbs = p?.envs.find((e) => e.name === env)?.services.filter((s) => isDb(s.kind)).map((s) => s.service) ?? [];
  const other = p?.envs.find((e) => e.name !== env)?.name;
  const [key, setKey] = useState("");
  const [value, setValue] = useState("");
  const [ref, setRefTo] = useState("");
  const [build, setBuild] = useState(false);
  const [shared, setShared] = useState(false);
  const [err, setErr] = useState("");
  const [shown, setShown] = useState<Record<string, string>>({});
  const [copied, setCopied] = useState("");
  const [filter, setFilter] = useState("");
  const [deploying, guardDeploy] = useBusy();
  const [del, setDel] = useState<Entry | null>(null);
  const [adding, setAdding] = useState(false);
  const [editing, setEditing] = useState(false);
  const [saving, save] = useBusy();
  const exists = !!list.data?.some((v) => v.key === key && v.scope === (shared ? "shared" : "service"));
  const timers = useRef<number[]>([]);
  const hideTimers = useRef(new Map<string, number>());
  useEffect(() => () => { timers.current.forEach(clearTimeout); hideTimers.current.forEach(clearTimeout); }, []);

  const reset = () => { setKey(""); setValue(""); setRefTo(""); setBuild(false); setShared(false); setEditing(false); setErr(""); };
  const close = () => { reset(); setAdding(false); };

  const add = (e: FormEvent) => { e.preventDefault(); return save(async () => {
    setErr("");
    try {
      const scope = shared ? envPath : svcPath;
      if (ref) await setRef(scope, key, ref);
      else await setVar(scope, key, value, build);
      close();
      list.reload();
    } catch (e) {
      setErr(errText(e));
    }
  }); };

  function edit(v: Entry) {
    reset();
    setKey(v.key); setRefTo(v.ref ?? ""); setBuild(!!v.build); setShared(v.scope === "shared");
    setEditing(true); setAdding(true);
  }

  const hide = (k: string) => {
    clearTimeout(hideTimers.current.get(k));
    hideTimers.current.delete(k);
    setShown((s) => { const n = { ...s }; delete n[k]; return n; });
  };

  async function show(k: string) {
    try {
      const v = await reveal(svcPath, k);
      setShown((s) => ({ ...s, [k]: v }));
      clearTimeout(hideTimers.current.get(k));
      hideTimers.current.set(k, window.setTimeout(() => hide(k), 30_000));
    } catch (e) {
      setErr(errText(e));
    }
  }

  async function copy(k: string) {
    setErr("");
    const value = reveal(svcPath, k);
    const written = writeClipboard(value).then(() => true, () => false);
    try {
      await value;
    } catch (e) {
      setErr(errText(e));
      return;
    }
    if (!(await written)) {
      setErr("The browser did not allow copying. Use Show and select the value.");
      return;
    }
    setCopied(k);
    timers.current.push(window.setTimeout(() => setCopied((c) => (c === k ? "" : c)), 1500));
  }

  const deploy = () => guardDeploy(async () => {
    setErr("");
    try {
      navigate(`/p/${project}/${env}/s/${service}/deploys/${await startDeploy(svcPath)}`);
    } catch (e) {
      setErr(errText(e));
    }
  });

  const q = filter.trim().toLowerCase();
  const own = list.data?.filter((v) => !GIVEN.has(v.scope)) ?? [];
  const given = list.data?.filter((v) => GIVEN.has(v.scope)) ?? [];
  const rows = own.filter((v) => v.key.toLowerCase().includes(q));

  return (
    <div className="flex flex-col gap-3">
      <div className="flex flex-wrap items-center justify-between gap-2.5">
        <p className="text-kumo-subtle">Sealed in this browser; the server never reads them. They apply at the next deploy.</p>
        <Button icon={<Icon name="plus" />} onClick={() => { reset(); setAdding(true); }}>Add a variable</Button>
      </div>
      {err && !adding && <Callout tone="bad">{err}</Callout>}
      {changes > 0 && (
        <Callout tone="warn">
          <span className="flex flex-wrap items-center justify-between gap-2.5">
            {`${changes} change${changes === 1 ? "" : "s"} not deployed yet`}
            {running
              ? <Link to={`/p/${project}/${env}/s/${service}/deploys/${running.id}`} className={linkVariants({ variant: "inline" })}>Deploying</Link>
              : <Button variant="primary" size="sm" disabled={deploying} onClick={deploy}>Deploy</Button>}
          </span>
        </Callout>
      )}
      {own.length > 0 && (
        <InputGroup className="w-full sm:w-72">
          <InputGroup.Addon><Icon name="search" /></InputGroup.Addon>
          <InputGroup.Input type="search" aria-label="Filter variables" placeholder={`Filter ${own.length} ${own.length === 1 ? "variable" : "variables"}`} value={filter} onChange={(e) => setFilter(e.target.value)} />
        </InputGroup>
      )}
      {!list.data ? <Loading what={list} /> : (
        <LayerCard className="overflow-x-auto p-0">
          <Table>
            <Table.Header><Table.Row><Table.Head>Key</Table.Head><Table.Head>Value</Table.Head><Table.Head /></Table.Row></Table.Header>
            <Table.Body>
              {own.length === 0 && <Table.Row><Table.Cell colSpan={3} className="text-kumo-subtle">No variables yet.</Table.Cell></Table.Row>}
              {own.length > 0 && rows.length === 0 && <Table.Row><Table.Cell colSpan={3} className="text-kumo-subtle">No variables match.</Table.Cell></Table.Row>}
              {rows.map((v) => (
                <Table.Row key={v.scope + v.key}>
                  <Table.Cell><span className="inline-flex flex-wrap items-center gap-1.5"><code>{v.key}</code>{v.scope !== "service" && <Badge variant="outline">{v.scope}</Badge>}{v.build && <Badge variant="outline">build</Badge>}{changed(v) && <Badge variant="outline">changed</Badge>}</span></Table.Cell>
                  <Table.Cell className="font-mono text-sm">{v.ref ? `→ ${v.ref}` : !(v.key in shown) ? "••••••••" : shown[v.key] === "" ? <span className="text-kumo-subtle">empty</span> : shown[v.key]}</Table.Cell>
                  <Table.Cell>
                    <div className="flex flex-wrap items-center justify-end gap-2">
                      {!v.ref && <>
                        {v.key in shown
                          ? <Button size="sm" aria-label={`Hide ${v.key}`} onClick={() => hide(v.key)}>Hide</Button>
                          : <Button size="sm" aria-label={`Show ${v.key}`} onClick={() => show(v.key)}>Show</Button>}
                        <Button size="sm" icon={<Icon name="copy" />} aria-label={`${copied === v.key ? "Copied" : "Copy"} ${v.key}`} onClick={() => copy(v.key)}>{copied === v.key ? "Copied" : "Copy"}</Button>
                      </>}
                      <Menu label={`Actions for ${v.key}`} trigger={<Icon name="more" />} size="sm">
                        <MenuItem onClick={() => edit(v)}>Edit value</MenuItem>
                        <MenuItem danger onClick={() => setDel(v)}>Delete</MenuItem>
                      </Menu>
                    </div>
                </Table.Cell>
                </Table.Row>
              ))}
            </Table.Body>
          </Table>
        </LayerCard>
      )}
      {list.data && <Provided port={view.data?.form.kind === "web" ? view.data.form.port : undefined} server={view.data?.server} given={given} own={own} />}
      {adding && <Modal title={editing ? `Edit ${key}` : "Add a variable"} onClose={close} dismissible={!saving} actions={
        <LayerDialog.Actions dismissLabel="Cancel">
          <LayerDialog.Action type="submit" form="env-var" disabled={saving}>{exists ? "Replace variable" : "Add variable"}</LayerDialog.Action>
        </LayerDialog.Actions>
      }>
        <form id="env-var" onSubmit={add} className="flex flex-col gap-4">
          <Field label="Key">{(id) => <Input id={id} className="font-mono" value={key} readOnly={editing} onChange={(e) => setKey(e.target.value.toUpperCase())} pattern="[A-Z_][A-Z0-9_]*" required />}</Field>
          {exists && !editing && <Callout tone="warn">{`${key} already exists. Adding replaces its value.`}</Callout>}
          {dbs.length > 0 && (
            <Field label="Reference" hint="A reference follows the database: change its password and every app gets the new URL.">
              {(id) => (
                <Select aria-labelledby={id + "-label"} value={ref} onValueChange={(v) => setRefTo(String(v))}
                  items={[{ value: "", label: "None, type a value" }, ...dbs.map((d) => ({ value: `${d}.url`, label: `${d}.url` }))]} />
              )}
            </Field>
          )}
          {!ref && <Field label="Value">{(id) => <SensitiveInput id={id} aria-labelledby={id + "-label"} className="font-mono" autoComplete="off" value={value} onValueChange={setValue} required />}</Field>}
          <div className="flex flex-col gap-3">
            {!ref && !view.data?.form.image && <Switch label="Also at build time" checked={build} onClick={() => setBuild(!build)} />}
            <Switch label={`Shared by every service in ${env}`} checked={shared} disabled={editing} onClick={() => setShared(!shared)} />
          </div>
          {err && <Callout tone="bad">{err}</Callout>}
        </form>
      </Modal>}
      {other && <Diff project={project} env={env} other={other} service={service} />}
      {del && (
        <Confirm title={`Delete ${del.key}?`} yes="Yes, delete" onYes={() => api("DELETE", `${del.scope === "shared" ? envPath : svcPath}/env/${del.key}`).then(list.reload)} onClose={() => setDel(null)}>
          <p>The running version keeps it until the next deploy.</p>
        </Confirm>
      )}
      <Sources project={project} env={env} />
    </div>
  );
}
