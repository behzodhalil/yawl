import { Banner, Button, ButtonGroup, DropdownMenu } from "@cloudflare/kumo";
import { DB_SHOWN, isDb } from "../../serviceForm";
import { useEffect, useState, type ReactNode } from "react";
import { api } from "../../api";
import { Shell } from "../../Shell";
import { recordRecent } from "../../palette";
import { startDeploy } from "../../serviceActions";
import { actorName, commitSubject, outsideLine, serviceWord, shortSha, stateKind } from "../../format";
import { navigate } from "../../router";
import type { Checks, DeployRow, Off, ProjectView, ServiceView } from "../../types";
import { Ago, Callout, Confirm, EnvName, Icon, Link, Loading, Menu, MenuItem, Modal, NavTabs, NoSignal, POLL_MS, StatusBadge, errText, useBusy, useLiveLoad, useLoad } from "../../ui";
import { CopyTo } from "./CopyTo";
import { Events, RollbackDialog } from "./Events";
import { Deploys } from "./Deploy";
import { Logs } from "./Logs";
import { Env } from "./Env";
import { ServiceSettings } from "./Settings";
import { DbInfo } from "./DbInfo";
import { Backups, VolumeBackups } from "./Backups";
import { Runs } from "./Runs";
import { Metrics } from "./Metrics";
import { Terminal } from "./Terminal";
import { Analytics } from "./Analytics";
import { Data } from "./Data";

type P = { project: string; env: string; service: string };

const TABS: Record<string, [string, string, (p: P) => ReactNode][]> = {
  app: [
    ["events", "Events", (p) => <Events {...p} />],
    ["logs", "Logs", (p) => <Logs {...p} />],
    ["metrics", "Metrics", (p) => <Metrics {...p} />],
    ["env", "Environment", (p) => <Env {...p} />],
    ["terminal", "Terminal", (p) => <Terminal {...p} />],
    ["settings", "Settings", (p) => <ServiceSettings {...p} />],
  ],
  cron: [
    ["events", "Events", (p) => <Events {...p} />],
    ["runs", "Runs", (p) => <Runs {...p} />],
    ["env", "Environment", (p) => <Env {...p} />],
    ["settings", "Settings", (p) => <ServiceSettings {...p} />],
  ],
  postgres: [
    ["info", "Info", (p) => <DbInfo {...p} />],
    ["backups", "Backups", (p) => <Backups {...p} />],
    ["terminal", "Terminal", (p) => <Terminal {...p} />],
  ],
};

const SLOW_OUTSIDE_MS = 1000;

const anyBusy = (ds: DeployRow[]) => ds.some((d) => stateKind(d.state) === "busy");

const DB_NAMES: Record<string, string> = { postgres: "Postgres", valkey: "Valkey", mysql: "MySQL", mongodb: "MongoDB", mariadb: "MariaDB", clickhouse: "ClickHouse" };

export function ServiceFrame({ p, tab, children }: { p: P; tab: string; children?: ReactNode }) {
  const view = useLoad<ServiceView>(`/api/services/${p.project}/${p.env}/${p.service}`, POLL_MS);
  const checks = useLoad<Checks | Off>("/api/checks");
  const check = checks.data && !("off" in checks.data) && view.data ? checks.data.results["service:" + view.data.id] : undefined;
  const outside = check ? { text: outsideLine(check), slow: check.ok && (check.ms ?? 0) > SLOW_OUTSIDE_MS } : undefined;
  const kind = view.data?.form.kind ?? "";
  useEffect(() => { if (kind) recordRecent({ project: p.project, env: p.env, service: p.service, kind }); }, [p.project, p.env, p.service, kind]);
  const volumes = view.data?.form.volumes ?? [];
  let tabs = TABS[isDb(kind) ? "postgres" : kind === "cron" ? kind : "app"];
  if (kind === "web") {
    const at = tabs.findIndex(([k]) => k === "metrics") + 1;
    tabs = [...tabs.slice(0, at), ["analytics", "Analytics", (p) => <Analytics {...p} />], ...tabs.slice(at)];
  }
  if (kind === "postgres" || kind === "mysql" || kind === "mariadb") {
    tabs = [tabs[0], ["data", "Data", (p) => <Data {...p} />], ...tabs.slice(1)];
  }
  if ((kind === "web" || kind === "worker") && volumes.length > 0) {
    const at = Math.max(...["metrics", "analytics"].map((k) => tabs.findIndex(([t]) => t === k))) + 1;
    tabs = [...tabs.slice(0, at), ["backups", "Backups", (q) => <VolumeBackups {...q} volumes={volumes} />], ...tabs.slice(at)];
  }
  const cur = tabs.find((t) => t[0] === tab) ?? tabs[0];
  const base = `/p/${p.project}/${p.env}/s/${p.service}`;
  const path = `/api/services/${p.project}/${p.env}/${p.service}`;
  const deploys = useLiveLoad<DeployRow[]>(view.data && !isDb(kind) ? path + "/deploys" : null, anyBusy);
  const live = !view.data?.stopped && view.data?.health !== "down" ? deploys.data?.find((d) => d.live) : undefined;
  const tree = useLoad<ProjectView[]>(view.data && isDb(kind) ? "/api/projects" : null, POLL_MS);
  const dbRow = tree.data?.find((x) => x.name === p.project)?.envs.find((e) => e.name === p.env)?.services.find((r) => r.service === p.service);
  const dbActive = !view.data?.stopped && view.data?.health !== "down";
  const dbWord = dbRow && dbActive ? serviceWord(kind, dbRow.state) : undefined;
  const dbPending = isDb(kind) && dbActive && !tree.data && !tree.error && !view.data?.server_offline;
  return (
    <Shell crumb={[<Link to="/">Projects</Link>, <Link to={`/p/${p.project}/${p.env}`}>{p.project}</Link>, <EnvName env={p.env} />, p.service]} active="/">
      {!view.data ? <Loading what={view} /> : (
        <>
          <div className="flex flex-col gap-1.5">
            <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
              <h1>{p.service}</h1>
              {view.data.stopped ? <StatusBadge kind="off">Stopped</StatusBadge> : view.data.health === "down" && <StatusBadge kind="bad">Down</StatusBadge>}
              {view.data.server_offline
                ? <NoSignal lastSeen={view.data.server_last_seen} />
                : dbPending ? <span className="inline-block h-6 w-22 animate-pulse rounded-md bg-kumo-fill" data-pending="" aria-hidden="true" /> : dbWord ? <StatusBadge kind={dbWord[1]}>{dbWord[0]}</StatusBadge> : live && <StatusBadge kind="ok">{serviceWord(kind, "live")[0]}</StatusBadge>}
              <span className="text-kumo-subtle">
                {isDb(kind) ? `${DB_NAMES[kind]} ${DB_SHOWN[kind] ?? view.data.form.version}` : kind === "web" ? "Web Service" : kind === "cron" ? "Cron Job" : "Background Worker"} on {view.data.server}
              </span>
              {view.data.url && <a href={view.data.url} target="_blank" rel="noreferrer" className="font-mono text-sm">{view.data.url}</a>}
              <Actions path={path} base={base} project={p.project} env={p.env} name={p.service} kind={kind} stopped={!!view.data.stopped} canRollBack={!!deploys.data?.some((d) => d.can_roll_back)} rollTo={deploys.data?.find((d) => d.rollback_target)} reload={view.reload} />
            </div>
            {(live || outside) && (
              <p className="text-sm text-kumo-subtle">
                {[
                  live && <span key="sha" className="font-mono">{shortSha(live.commit) || live.digest?.slice(7, 19)}</span>,
                  live?.subject && <span key="subject">{commitSubject(live.subject)}</span>,
                  live && <Ago key="ago" ms={live.created_at} />,
                  live?.by && <span key="by">{`by ${actorName(live.by)}`}</span>,
                  outside && <span key="outside" className={outside.slow ? "text-kumo-warning" : undefined}>{outside.text}</span>,
                ].filter(Boolean).flatMap((x, i) => (i ? [" · ", x] : [x]))}
              </p>
            )}
          </div>
          {!view.data.stopped && <Waiting path={path} base={base} settingsAt={view.data.settings_at} deploys={deploys.data} withVars={cur[0] !== "env" || !!children} />}
          <NavTabs label="Service" tabs={tabs.map(([k, label]) => ({ to: `${base}/${k}`, label, current: !children && cur[0] === k }))} />
          <Deploys.Provider value={deploys}>{children ?? cur[2](p)}</Deploys.Provider>
        </>
      )}
    </Shell>
  );
}

function Waiting({ path, base, settingsAt, deploys, withVars }: { path: string; base: string; settingsAt?: number; deploys?: DeployRow[]; withVars: boolean }) {
  const vars = useLoad<{ key: string; updated_at: number }[]>(withVars ? path + "/env" : null);
  const [busy, once] = useBusy();
  const [err, setErr] = useState("");
  const liveAt = deploys?.find((d) => d.live)?.created_at;
  if (liveAt === undefined || deploys!.some((d) => d.created_at > liveAt && stateKind(d.state) === "busy")) return null;
  const items = [
    ...(withVars ? vars.data ?? [] : []).filter((v) => v.updated_at > liveAt).map((v) => `${v.key} set`),
    ...(settingsAt && settingsAt > liveAt ? ["Settings changed"] : []),
  ];
  if (items.length === 0) return null;
  const deploy = () => once(async () => {
    setErr("");
    try { navigate(`${base}/deploys/${await startDeploy(path)}`); } catch (e) { setErr(errText(e)); }
  });
  return (
    <Banner title={`${items.length} ${items.length === 1 ? "change waits" : "changes wait"} for a deploy`} description={err || items.join(" · ")}
      action={<Button variant="primary" size="sm" disabled={busy} onClick={deploy}>Deploy</Button>} />
  );
}

function Actions({ path, base, project, env, name, kind, stopped, canRollBack, rollTo, reload }: { path: string; base: string; project: string; env: string; name: string; kind: string; stopped: boolean; canRollBack: boolean; rollTo?: DeployRow; reload: () => void }) {
  const [asking, setAsking] = useState<string[] | null>(null);
  const [rolling, setRolling] = useState(false);
  const [copying, setCopying] = useState(false);
  const [err, setErr] = useState("");
  const [busy, once] = useBusy();
  const builds = !isDb(kind);
  const act = (what: string) => once(async () => {
    setErr("");
    try { await api("POST", `${path}/${what}`); reload(); } catch (e) { setErr(errText(e)); }
  });
  const deploy = (noCache: boolean) => once(async () => {
    setErr("");
    try {
      navigate(`${base}/deploys/${await startDeploy(path, noCache)}`);
    } catch (e) {
      setErr(errText(e));
    }
  });
  const askStop = async () => {
    let users: string[] = [];
    if (isDb(kind)) {
      try { users = (await api<{ users: string[] }>("GET", path + "/delete-info")).users ?? []; } catch {}
    }
    setAsking(users);
  };
  return (
    <div className="ml-auto flex flex-col items-end gap-1.5">
      <ButtonGroup>
        {stopped ? <Button variant="primary" disabled={busy} onClick={() => act("start")}>Start</Button>
          : builds && <Button variant="primary" disabled={busy} onClick={() => deploy(false)}>Deploy</Button>}
        <Menu label={builds && !stopped ? "Deploy options" : "More actions"} trigger={<Icon name={builds && !stopped ? "down" : "more"} />} variant={builds && !stopped ? "primary" : "secondary"}>
          {builds && !stopped && <><MenuItem icon="build" description="Builds every layer again from scratch." onClick={() => deploy(true)}>Deploy without cache</MenuItem><DropdownMenu.Separator /></>}
          {!stopped && <MenuItem icon="restart" description="The running version starts again; nothing is built." onClick={() => act("restart")}>Restart</MenuItem>}
          {(!builds || canRollBack) && <MenuItem icon="history" description="Goes back to the previous version." onClick={() => setRolling(true)}>Roll back…</MenuItem>}
          <MenuItem icon="copy" description="To another environment or project." onClick={() => setCopying(true)}>Copy or move…</MenuItem>
          {!stopped && <MenuItem danger icon="status-off" description="Stops serving until you start it again." onClick={askStop}>Stop</MenuItem>}
        </Menu>
      </ButtonGroup>
      {err && <Callout tone="bad">{err}</Callout>}
      {rolling && (
        <RollbackDialog path={path} base={base} service={name} to={rollTo} onClose={() => setRolling(false)}>
          <p>{builds ? "The database is not touched. Migrations that ran stay in place." : "Runs the previous image on the same data. An older major version may not start on data an upgrade changed."}</p>
          {stopped && <p>It starts again.</p>}
        </RollbackDialog>
      )}
      {copying && (
        <Modal title={`Copy or move ${name}`} onClose={() => setCopying(false)}>
          <CopyTo project={project} env={env} service={name} />
        </Modal>
      )}
      {asking && (
        <Confirm title={`Stop ${name}?`} yes="Yes, stop" onYes={() => api("POST", path + "/stop").then(reload)} onClose={() => setAsking(null)}>
          <p>It stops serving until you start it. Its data, variables and backups stay.</p>
          {asking.length > 0 && <p>{`${asking.join(", ")} use${asking.length === 1 ? "s" : ""} it and will fail while it is stopped.`}</p>}
        </Confirm>
      )}
    </div>
  );
}
