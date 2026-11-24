import { DB_SHOWN, isDb } from "../../serviceForm";
import { useEffect, useState, type ReactNode } from "react";
import { api } from "../../api";
import { Shell } from "../../Shell";
import { recordRecent } from "../../palette";
import { startDeploy } from "../../serviceActions";
import { actorName, commitSubject, outsideLine, serviceWord, shortSha, stateKind } from "../../format";
import { navigate } from "../../router";
import type { Checks, DeployRow, Off, ProjectView, ServiceView } from "../../types";
import { Ago, Callout, Confirm, EnvName, Icon, Link, Loading, Menu, MenuItem, NoSignal, POLL_MS, StatusBadge, errText, useBusy, useLiveLoad, useLoad } from "../../ui";
import { Events } from "./Events";
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
    ["runs", "Runs", (p) => <Runs {...p} />],
    ["env", "Environment", (p) => <Env {...p} />],
    ["settings", "Settings", (p) => <ServiceSettings {...p} />],
    ["events", "Events", (p) => <Events {...p} />],
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
          <div className="svc-head">
            <div className="svc-row">
              <h1>{p.service}</h1>
              {view.data.stopped ? <StatusBadge kind="off">Stopped</StatusBadge> : view.data.health === "down" && <StatusBadge kind="bad">Down</StatusBadge>}
              {view.data.server_offline
                ? <NoSignal lastSeen={view.data.server_last_seen} />
                : dbPending ? <span className="badge skel-chip" aria-hidden="true" /> : dbWord ? <StatusBadge kind={dbWord[1]}>{dbWord[0]}</StatusBadge> : live && <StatusBadge kind="ok">{serviceWord(kind, "live")[0]}</StatusBadge>}
              <span className="muted">
                {isDb(kind) ? `${DB_NAMES[kind]} ${DB_SHOWN[kind] ?? view.data.form.version}` : kind === "web" ? "Web Service" : kind === "cron" ? "Cron Job" : "Background Worker"} on {view.data.server}
              </span>
              {view.data.url && <a href={view.data.url} target="_blank" rel="noreferrer" className="mono">{view.data.url}</a>}
              <Actions path={path} base={base} name={p.service} kind={kind} stopped={!!view.data.stopped} reload={view.reload} />
            </div>
            {(live || outside) && (
              <p className="svc-meta muted">
                {[
                  live && <span key="sha" className="mono">{shortSha(live.commit) || live.digest?.slice(7, 19)}</span>,
                  live?.subject && <span key="subject">{commitSubject(live.subject)}</span>,
                  live && <Ago key="ago" ms={live.created_at} />,
                  live?.by && <span key="by">{`by ${actorName(live.by)}`}</span>,
                  outside && <span key="outside" className={outside.slow ? "slow" : ""}>{outside.text}</span>,
                ].filter(Boolean).flatMap((x, i) => (i ? [" · ", x] : [x]))}
              </p>
            )}
          </div>
          <div className="tabs">
            {tabs.map(([k, label]) => <Link key={k} to={`${base}/${k}`} className={!children && cur[0] === k ? "on" : ""}>{label}</Link>)}
          </div>
          <Deploys.Provider value={deploys}>{children ?? cur[2](p)}</Deploys.Provider>
        </>
      )}
    </Shell>
  );
}

function Actions({ path, base, name, kind, stopped, reload }: { path: string; base: string; name: string; kind: string; stopped: boolean; reload: () => void }) {
  const [asking, setAsking] = useState<string[] | null>(null);
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
    <div className="col" style={{ gap: 6, alignItems: "flex-end" }}>
      {stopped ? <button className="btn pri" disabled={busy} onClick={() => act("start")}>Start</button> : (
        <div className="split">
          {builds && <button className="btn pri" disabled={busy} onClick={() => deploy(false)}>Deploy</button>}
          <Menu label={builds ? "Deploy options" : "More actions"} trigger={<Icon name={builds ? "down" : "more"} />} className={builds ? "btn pri ib" : "btn ib"}>
            {builds && <><MenuItem onClick={() => deploy(true)}>Deploy without cache</MenuItem><hr /></>}
            <MenuItem onClick={() => act("restart")}>Restart</MenuItem>
            <MenuItem danger onClick={askStop}>Stop</MenuItem>
          </Menu>
        </div>
      )}
      {err && <Callout tone="bad">{err}</Callout>}
      {asking && (
        <Confirm title={`Stop ${name}?`} yes="Yes, stop" onYes={() => api("POST", path + "/stop").then(reload)} onClose={() => setAsking(null)}>
          <p>It stops serving until you start it. Its data, variables and backups stay.</p>
          {asking.length > 0 && <p>{`${asking.join(", ")} use${asking.length === 1 ? "s" : ""} it and will fail while it is stopped.`}</p>}
        </Confirm>
      )}
    </div>
  );
}
