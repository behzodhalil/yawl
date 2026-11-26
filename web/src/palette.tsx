import { CommandPalette, DialogTitle } from "@cloudflare/kumo";
import { useEffect, useMemo, useRef, useState } from "react";
import { navigate } from "./router";
import { isDb } from "./serviceForm";
import { api } from "./api";
import { startDeploy } from "./serviceActions";
import { Icon, errText, useLoad } from "./ui";
import type { ProjectView, ServerView } from "./types";

export type Place = { to: string; label: string; icon: string; hint?: string };
export type Visit = { project: string; env: string; service: string; kind: string };
type Row = Place & { group: string; run?: () => Promise<string>; doing?: string };
type Group = { label: string; items: Row[] };

const RECENT_KEY = "yawl.recent";
const RECENT_MAX = 5;

export function readRecent(): Visit[] {
  try {
    const list = JSON.parse(localStorage.getItem(RECENT_KEY) ?? "[]");
    return Array.isArray(list) ? list.filter((v) => v && ["project", "env", "service", "kind"].every((k) => typeof v[k] === "string")).slice(0, RECENT_MAX) : [];
  } catch {
    return [];
  }
}

export function recordRecent(v: Visit) {
  try {
    const rest = readRecent().filter((x) => !(x.project === v.project && x.env === v.env && x.service === v.service));
    localStorage.setItem(RECENT_KEY, JSON.stringify([v, ...rest].slice(0, RECENT_MAX)));
  } catch {}
}

const kindIcon = (kind: string) => (kind === "web" ? "globe" : kind);
const servicePlace = (v: Visit, group: string): Row => ({ group, to: `/p/${v.project}/${v.env}/s/${v.service}`, label: v.service, icon: kindIcon(v.kind), hint: `${v.project} / ${v.env}` });

export function Palette({ pages, onClose }: { pages: Place[]; onClose: () => void }) {
  const projects = useLoad<ProjectView[]>("/api/projects");
  const servers = useLoad<ServerView[]>("/api/servers");
  const [q, setQ] = useState("");

  const recent = useMemo(readRecent, []);

  const catalog = useMemo(() => {
    const services: Row[] = [], envs: Row[] = [], actions: Row[] = [];
    for (const p of projects.data ?? []) {
      for (const e of p.envs) {
        envs.push({ group: "Results", to: `/p/${p.name}/${e.name}`, label: `${p.name} / ${e.name}`, icon: "layers", hint: "Environment" });
        for (const s of e.services) {
          const v = { project: p.name, env: e.name, service: s.service, kind: s.kind };
          const place = servicePlace(v, "Results");
          services.push(place);
          const path = `/api/services/${p.name}/${e.name}/${s.service}`;
          const hint = place.hint;
          if (!isDb(s.kind) && !s.stopped) actions.push({ group: "Actions", to: place.to, label: `Deploy ${s.service}`, icon: "rocket", hint, doing: "Deploying…", run: async () => `${place.to}/deploys/${await startDeploy(path)}` });
          if (!isDb(s.kind) && s.kind !== "cron") actions.push({ group: "Actions", to: place.to, label: `View logs for ${s.service}`, icon: "logs", hint, run: async () => `${place.to}/logs` });
          if (!s.stopped) actions.push({ group: "Actions", to: place.to, label: `Restart ${s.service}`, icon: "restart", hint, doing: "Restarting…", run: async () => { await api("POST", path + "/restart"); return place.to; } });
        }
      }
    }
    const serverRows: Row[] = (servers.data ?? []).map((s) => ({ group: "Results", to: `/servers/${encodeURIComponent(s.name)}`, label: s.name, icon: "server", hint: "Server" }));
    return { services, envs, servers: serverRows, actions };
  }, [projects.data, servers.data]);

  const shown = useMemo(() => {
    const t = q.trim().toLowerCase();
    const pageRows: Row[] = pages.map((x) => ({ ...x, group: t ? "Results" : "Pages" }));
    const recentRows = recent.map((v) => servicePlace(v, "Recent"));
    if (!t) return [...recentRows, ...pageRows];
    const match = (x: Row) => (x.label + " " + (x.hint ?? "")).toLowerCase().includes(t);
    const recentHits = recentRows.filter(match);
    const seen = new Set(recentHits.map((x) => x.to));
    const rest = [...catalog.services, ...catalog.envs, ...catalog.servers, ...pageRows].filter((x) => match(x) && !seen.has(x.to));
    return [...recentHits, ...rest, ...catalog.actions.filter(match)];
  }, [catalog, pages, recent, q]);
  const groups = useMemo(() => shown.reduce<Group[]>((out, x) => {
    const last = out[out.length - 1];
    if (last && last.label === x.group) last.items.push(x); else out.push({ label: x.group, items: [x] });
    return out;
  }, []), [shown]);

  const alive = useRef(true);
  useEffect(() => { alive.current = true; return () => { alive.current = false; }; }, []);
  const [running, setRunning] = useState("");
  const [failed, setFailed] = useState("");
  const err = projects.error ?? servers.error;
  const rowKey = (x: Row) => x.group + x.label + x.to;
  const go = async (x?: Row) => {
    if (!x || running) return;
    if (!x.run) { onClose(); navigate(x.to); return; }
    setFailed("");
    setRunning(rowKey(x));
    try {
      const to = await x.run();
      if (!alive.current) return;
      onClose();
      navigate(to);
    } catch (e) {
      setFailed(errText(e));
      setRunning("");
    }
  };
  return (
    <CommandPalette.Root open onOpenChange={(open: boolean) => { if (!open) onClose(); }} items={groups} value={q} onValueChange={setQ}
      itemToStringValue={(g: Group) => g.label} filter={() => true} getSelectableItems={(gs: Group[]) => gs.flatMap((g) => g.items)} onSelect={(x: Row) => go(x)}>
      <DialogTitle className="sr-only">Go to</DialogTitle>
      <CommandPalette.Input aria-label="Go to" placeholder="Go to a page, service or server, or run an action" />
      <CommandPalette.List>
        <CommandPalette.Results>
          {(g: Group) => (
            <CommandPalette.Group key={g.label} items={g.items}>
              <CommandPalette.GroupLabel>{g.label}</CommandPalette.GroupLabel>
              <CommandPalette.Items>
                {(x: Row) => (
                  <CommandPalette.Item key={rowKey(x)} value={x} onClick={() => go(x)}>
                    <span className="flex w-full items-center gap-2.5">
                      <Icon name={x.icon} /><span>{running === rowKey(x) && x.doing ? x.doing : x.label}</span>
                      {x.hint && <span className="ml-auto text-sm text-kumo-subtle">{x.hint}</span>}
                    </span>
                  </CommandPalette.Item>
                )}
              </CommandPalette.Items>
            </CommandPalette.Group>
          )}
        </CommandPalette.Results>
        <CommandPalette.Empty>{(projects.data && servers.data) || err ? "Nothing matches." : "Looking…"}</CommandPalette.Empty>
      </CommandPalette.List>
      {(failed || err) && <p className="px-3.5 py-2 text-kumo-danger" role="alert">{failed || err}</p>}
      <span className="sr-only" aria-live="polite">{shown.length === 1 ? "1 result" : `${shown.length} results`}</span>
      <CommandPalette.Footer>
        <span className="flex items-center gap-4">
          <span className="flex items-center gap-2"><kbd className="rounded-sm border border-kumo-hairline px-1.5 font-mono text-xs">↑↓</kbd>Move</span>
          <span className="flex items-center gap-2"><kbd className="rounded-sm border border-kumo-hairline px-1.5 font-mono text-xs">↵</kbd>Open</span>
          <span className="flex items-center gap-2"><kbd className="rounded-sm border border-kumo-hairline px-1.5 font-mono text-xs">Esc</kbd>Close</span>
        </span>
      </CommandPalette.Footer>
    </CommandPalette.Root>
  );
}
