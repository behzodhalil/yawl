import { useContext, useEffect, useState, type ReactNode } from "react";
import { SessionContext } from "./session";
import { Icon, Link, POLL_MS, useLoad } from "./ui";
import type { Incident, ServerView } from "./types";
import { Palette, type Place } from "./palette";

const offlineText = (n: number) => `${n} ${n === 1 ? "server" : "servers"} offline`;

const NAV: [string, string, string][] = [
  ["/", "Projects", "layers"],
  ["/servers", "Servers", "server"],
  ["/shared", "Shared variables", "key"],
  ["/incidents", "Incidents", "alert"],
  ["/status-pages", "Status pages", "pulse"],
  ["/integrations", "Integrations", "plug"],
  ["/notifications", "Notifications", "bell"],
  ["/team", "Team", "users"],
  ["/activity", "Activity", "history"],
  ["/settings", "Settings", "cog"],
];
const PAGES: Place[] = [...NAV.map(([to, label, icon]) => ({ to, label, icon })), { to: "/account", label: "Account", icon: "person" }];

const THEME = "yawl.theme";
const ACCENT = "yawl.accent";
const LOOK = "yawl:look";
const root = document.documentElement;
export const ACCENTS = [["lime", "Lime"], ["red-orange", "Red-orange"], ["blue", "Blue"], ["violet", "Violet"], ["pink", "Pink"], ["ink", "Ink"]] as const;
export type Accent = (typeof ACCENTS)[number][0];
export type Theme = "light" | "dark" | "system";
const isAccent = (a: unknown): a is Accent => ACCENTS.some(([k]) => k === a);
try {
  const t = localStorage.getItem(THEME);
  if (t === "light" || t === "dark") root.dataset.theme = t;
  const a = localStorage.getItem(ACCENT);
  if (isAccent(a)) root.dataset.accent = a;
} catch { /* storage off */ }

function themeNow(): "light" | "dark" {
  const t = root.dataset.theme;
  if (t === "light" || t === "dark") return t;
  return window.matchMedia?.("(prefers-color-scheme: light)").matches ? "light" : "dark";
}

export function pickTheme(t: Theme) {
  if (t === "system") delete root.dataset.theme; else root.dataset.theme = t;
  try { if (t === "system") localStorage.removeItem(THEME); else localStorage.setItem(THEME, t); } catch { /* storage off */ }
  window.dispatchEvent(new Event(LOOK));
}

export function pickAccent(a: Accent) {
  root.dataset.accent = a;
  try { localStorage.setItem(ACCENT, a); } catch { /* storage off */ }
  window.dispatchEvent(new Event(LOOK));
}

export function useLook(): { theme: Theme; accent: Accent } {
  const [, bump] = useState(0);
  useEffect(() => {
    const f = () => bump((n) => n + 1);
    window.addEventListener(LOOK, f);
    return () => window.removeEventListener(LOOK, f);
  }, []);
  const t = root.dataset.theme, a = root.dataset.accent;
  return { theme: t === "light" || t === "dark" ? t : "system", accent: isAccent(a) ? a : "lime" };
}

function useSignals(): { incidents: number; offline: number } {
  const incidents = useLoad<Incident[] | { off: true }>("/api/incidents?days=90", POLL_MS).data;
  const servers = useLoad<ServerView[]>("/api/servers", POLL_MS).data;
  return {
    incidents: Array.isArray(incidents) ? incidents.filter((i) => i.closed_at === null).length : 0,
    offline: Array.isArray(servers) ? servers.filter((s) => s.offline).length : 0,
  };
}

export function Shell({ crumb, active, children }: { crumb: ReactNode[]; active: string; children: ReactNode }) {
  const session = useContext(SessionContext);
  useLook();
  const theme = themeNow();
  const signals = useSignals();
  const [finding, setFinding] = useState(false);
  useEffect(() => {
    const key = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
        e.preventDefault();
        setFinding((f) => f ? false : !document.querySelector(".modal-bg"));
      }
    };
    window.addEventListener("keydown", key);
    return () => window.removeEventListener("keydown", key);
  }, []);
  const flip = () => pickTheme(theme === "dark" ? "light" : "dark");
  return (
    <div className="app">
      <header className="top">
        <Link to="/" className="brand"><span className="mark" aria-hidden="true">Y</span>Yawl</Link>
        <nav className="crumb" aria-label="Breadcrumb">
          {crumb.map((c, i) => (
            <span key={i} className="row" style={{ gap: 8 }}>
              {i > 0 && <span className="dim">/</span>}
              {i === crumb.length - 1 ? <b>{c}</b> : c}
            </span>
          ))}
        </nav>
        <nav className="mnav" aria-label="Sections">
          {NAV.map(([to, label]) => <Link key={to} to={to} className={active === to ? "on" : ""}>{label}</Link>)}
        </nav>
        <button type="button" className="tbtn" aria-label="Search" onClick={() => setFinding(true)}><Icon name="search" /><span className="hide">Search<span className="kbd">⌘K</span></span></button>
        <button type="button" className="tbtn" aria-label={theme === "dark" ? "Light theme" : "Dark theme"} onClick={flip}><Icon name={theme === "dark" ? "sun" : "theme"} /></button>
        <Link to="/account" className={"tbtn" + (active === "/account" ? " on" : "")}><Icon name="person" />Account</Link>
      </header>
      <nav className="side" aria-label="Main">
        {NAV.map(([to, label, icon]) => (
          <Link key={to} to={to} className={active === to ? "on" : ""}><Icon name={icon} />{label}
            {to === "/incidents" && signals.incidents > 0 && <span className="cnt">{signals.incidents}</span>}
            {to === "/servers" && signals.offline > 0 && <span className="sq" title={offlineText(signals.offline)}><span className="sr">{offlineText(signals.offline)}</span></span>}
          </Link>
        ))}
      </nav>
      <main><div className="page">
        {session.adopting && (
          <div className="callout warn" role="status">
            <span style={{ flex: 1 }}>This panel was restored from a backup. It changes nothing until you review what the servers run.</span>
            <Link to="/adopt" className="btn sm">Review</Link>
          </div>
        )}
        {session.managed && <BillingLine />}
        {children}
      </div></main>
      {finding && <Palette pages={PAGES} onClose={() => setFinding(false)} />}
    </div>
  );
}

type Billing = { state: string; trial_ends?: number; deploys: boolean; card_url?: string };

function BillingLine() {
  const b = useLoad<Billing>("/api/billing").data;
  if (!b?.card_url) return null;
  const card = (label: string) => <a className="btn sm" href={b.card_url}>{label}</a>;
  if (!b.deploys) {
    return <div className="callout warn" role="status"><span style={{ flex: 1 }}>New deploys are paused until this panel's subscription is paid; running apps keep running.</span>{card("Add a card")}</div>;
  }
  if (b.state === "past_due") {
    return <div className="callout warn" role="status"><span style={{ flex: 1 }}>Your last payment did not go through.</span>{card("Update the card")}</div>;
  }
  if (b.state === "trialing" && b.trial_ends) {
    const days = Math.max(0, Math.ceil((b.trial_ends - Date.now()) / 864e5));
    return <div className="callout" role="status"><span style={{ flex: 1 }}>Trial: {days} {days === 1 ? "day" : "days"} left.</span>{card("Add a card")}</div>;
  }
  return null;
}
