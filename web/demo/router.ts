import { useSyncExternalStore } from "react";
import { route } from "../src/router";

export type { Route } from "../src/router";
export { route };

const path = () => location.hash.slice(1) || "/";
export const fragment = () => path().split("#")[1] ?? "";
const listeners = new Set<() => void>();
const notify = () => listeners.forEach((l) => l());
window.addEventListener("hashchange", notify);
window.addEventListener("popstate", notify);

export function navigate(to: string, replace = false) {
  if (to === path()) return;
  if (replace) history.replaceState(null, "", "#" + to);
  else history.pushState(null, "", "#" + to);
  notify();
}

export function useRoute() {
  return route(useSyncExternalStore((l) => (listeners.add(l), () => listeners.delete(l)), path).split("#")[0]);
}
