import { useSyncExternalStore } from "react";

export type Route = { page: string; params: Record<string, string> };

const PATTERNS: [string, string][] = [
  ["/claim", "claim"],
  ["/invite", "invite"],
  ["/reset", "reset"],
  ["/sign-in", "sign-in"],
  ["/", "projects"],
  ["/projects", "projects"],
  ["/projects/new", "new-project"],
  ["/p/:project/:env", "project"],
  ["/p/:project/:env/new/?:kind", "new-service"],
  ["/p/:project/:env/templates", "templates"],
  ["/p/:project/:env/move", "move"],
  ["/p/:project/:env/pulls", "pulls"],
  ["/p/:project/:env/s/:service/deploys/:id", "deploy"],
  ["/p/:project/:env/s/:service/?:tab", "service"],
  ["/servers", "servers"],
  ["/servers/new", "add-server"],
  ["/servers/:name", "server"],
  ["/servers/:name/firewall", "firewall"],
  ["/servers/:name/disk", "disk"],
  ["/servers/:name/tailscale", "tailscale"],
  ["/servers/:name/os", "os"],
  ["/servers/:name/drain", "drain"],
  ["/incidents", "incidents"],
  ["/status-pages", "status-pages"],
  ["/status-pages/:id", "status-page"],
  ["/settings/?:section", "settings"],
  ["/integrations/?:section", "integrations"],
  ["/notifications/?:section", "notifications"],
  ["/team/?:section", "team"],
  ["/activity/?:section", "activity"],
  ["/shared", "shared"],
  ["/account", "account"],
  ["/adopt", "adopt"],
  ["/cli-login", "cli-login"],
];

export function route(path: string): Route {
  const parts = path.split("/").filter(Boolean);
  for (const [pattern, page] of PATTERNS) {
    const want = pattern.split("/").filter(Boolean);
    const optional = want.length > 0 && want[want.length - 1].startsWith("?:");
    if (parts.length !== want.length && !(optional && parts.length === want.length - 1)) continue;
    const params: Record<string, string> = {};
    const ok = want.every((w, i) => {
      if (w.startsWith("?:")) return (params[w.slice(2)] = parts[i] ?? ""), true;
      if (w.startsWith(":")) return parts[i] !== undefined && ((params[w.slice(1)] = decodeURIComponent(parts[i])), true);
      return w === parts[i];
    });
    if (ok) return { page, params };
  }
  return { page: "not-found", params: {} };
}

export const fragment = () => location.hash.slice(1);

const listeners = new Set<() => void>();
const notify = () => listeners.forEach((l) => l());
window.addEventListener("popstate", notify);

export function navigate(path: string, replace = false) {
  if (path === location.pathname + location.hash) return;
  if (replace) history.replaceState(null, "", path);
  else history.pushState(null, "", path);
  notify();
}

export function useRoute(): Route {
  const path = useSyncExternalStore(
    (l) => (listeners.add(l), () => listeners.delete(l)),
    () => location.pathname,
  );
  return route(path);
}
