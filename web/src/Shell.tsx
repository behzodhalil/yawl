import { useContext, useEffect, useRef, useState, type ReactNode } from "react";
import { Badge, Banner, Button, LinkProvider, Sidebar, Toasty, cn } from "@cloudflare/kumo";
import { SessionContext } from "./session";
import { ButtonLink, Icon, Link, POLL_MS, toasts, useLoad } from "./ui";
import type { Incident, ServerView } from "./types";
import { Palette, type Place } from "./palette";

const offlineText = (n: number) => `${n} ${n === 1 ? "server" : "servers"} offline`;

type NavItem = [string, string, string];
const NAV_GROUPS: [string, NavItem[]][] = [
  ["Hosting", [["/", "Projects", "folders"], ["/servers", "Servers", "server"], ["/shared", "Shared variables", "braces"]]],
  ["Monitoring", [["/incidents", "Incidents", "siren"], ["/status-pages", "Status pages", "broadcast"]]],
  ["Panel", [["/integrations", "Integrations", "puzzle"], ["/notifications", "Notifications", "bell-simple"], ["/team", "Team", "users-three"], ["/activity", "Activity", "activity-list"], ["/settings", "Settings", "gear-six"]]],
];
const NAV = NAV_GROUPS.flatMap(([, items]) => items);
const WIDE_ONLY = "group-data-[state=collapsed]/sidebar:hidden";
const ICON_ROW = "group-data-[state=collapsed]/sidebar:min-h-9";
const SEARCH = ICON_ROW + " min-h-[38px] text-[15px] font-medium text-kumo-subtle [&_.ico]:size-[18px] bg-kumo-base ring ring-kumo-line hover:bg-kumo-base hover:text-kumo-default "
  + "group-data-[state=collapsed]/sidebar:bg-transparent group-data-[state=collapsed]/sidebar:ring-0 group-data-[state=collapsed]/sidebar:hover:bg-kumo-tint/60";
const NAV_ITEM = ICON_ROW + " min-h-[38px] text-[15px] font-medium text-kumo-subtle hover:bg-kumo-tint/60 hover:text-kumo-default "
  + "focus-visible:outline-hidden focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-kumo-brand data-[active]:focus-visible:ring-2 data-[active]:focus-visible:ring-kumo-brand "
  + "group-data-[state=collapsed]/sidebar:data-[active]:ring-inset "
  + "data-[active]:bg-kumo-control data-[active]:font-semibold data-[active]:text-kumo-default data-[active]:ring-1 data-[active]:ring-kumo-line "
  + "data-[active]:shadow-[inset_0_1px_0_rgb(255_255_255/0.06),0_1px_2px_rgb(0_0_0/0.35)] data-[active]:hover:bg-kumo-elevated "
  + "[&_.ico]:size-[18px] [&_.ico]:text-kumo-subtle data-[active]:[&_.ico]:text-kumo-default";
const PAGES: Place[] = [...NAV.map(([to, label, icon]) => ({ to, label, icon })), { to: "/account", label: "Account", icon: "person" }];

export const OPEN_SEARCH = "yawl:search";
const THEME = "yawl.theme";
const LOOK = "yawl:look";
const SIDEBAR = "yawl.sidebar";

function keptOpen(): boolean {
  try { return localStorage.getItem(SIDEBAR) !== "closed"; } catch { return true; }
}

function keepOpen(open: boolean) {
  try { if (open) localStorage.removeItem(SIDEBAR); else localStorage.setItem(SIDEBAR, "closed"); } catch { /* storage off */ }
}
const root = document.documentElement;
export type Theme = "light" | "dark" | "system";
const lightQuery = window.matchMedia?.("(prefers-color-scheme: light)");

function kept(): Theme {
  try {
    const t = localStorage.getItem(THEME);
    if (t === "light" || t === "dark") return t;
  } catch { /* storage off */ }
  return "system";
}

let theme: Theme = kept();

function applyMode() {
  root.dataset.mode = theme === "system" ? (lightQuery?.matches ? "light" : "dark") : theme;
}
applyMode();
lightQuery?.addEventListener?.("change", () => {
  applyMode();
  window.dispatchEvent(new Event(LOOK));
});

export function useDarkMode(): boolean {
  useLook();
  return themeNow() === "dark";
}

function themeNow(): "light" | "dark" {
  return root.dataset.mode === "light" ? "light" : "dark";
}

export function pickTheme(t: Theme) {
  theme = t;
  applyMode();
  try { if (t === "system") localStorage.removeItem(THEME); else localStorage.setItem(THEME, t); } catch { /* storage off */ }
  window.dispatchEvent(new Event(LOOK));
}

export function useLook(): { theme: Theme } {
  const [, bump] = useState(0);
  useEffect(() => {
    const f = () => bump((n) => n + 1);
    window.addEventListener(LOOK, f);
    return () => window.removeEventListener(LOOK, f);
  }, []);
  return { theme };
}

function useVersion(): string | undefined {
  const current = useLoad<{ current: string }>("/api/update").data?.current;
  return current && (current === "dev" ? "dev" : "v" + current);
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
  const version = useVersion();
  const [finding, setFinding] = useState(false);
  const nav = useRef<HTMLElement>(null);
  useEffect(() => {
    nav.current?.querySelector("[aria-current=page]")?.scrollIntoView?.({ block: "nearest" });
  }, [active]);
  useEffect(() => {
    const key = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
        e.preventDefault();
        setFinding((f) => f ? false : !document.querySelector("[role=dialog]"));
      }
    };
    const open = () => setFinding(true);
    window.addEventListener("keydown", key);
    window.addEventListener(OPEN_SEARCH, open);
    return () => (window.removeEventListener("keydown", key), window.removeEventListener(OPEN_SEARCH, open));
  }, []);
  const flip = () => pickTheme(theme === "dark" ? "light" : "dark");
  const search = () => setFinding(true);
  return (
    <LinkProvider component={Link}>
    <Toasty toastManager={toasts}>
    <Sidebar.Provider collapsible="icon" defaultOpen={keptOpen()} onOpenChange={keepOpen} className="h-full min-h-0">
      <Sidebar>
        <Sidebar.Header className="flex h-14 flex-row items-center gap-2.5 border-b border-kumo-line px-4">
          <Link to="/" className="flex min-w-0 items-center gap-2.5 text-base font-semibold"><span className="grid size-6 shrink-0 place-items-center rounded-sm bg-kumo-contrast font-mono text-xs text-kumo-inverse" aria-hidden="true">Y</span><span className={WIDE_ONLY}>Yawl</span></Link>
          {version && <span className={cn("ml-auto font-mono text-xs text-kumo-subtle", WIDE_ONLY)}>{version}</span>}
        </Sidebar.Header>
        <Sidebar.Content>
          <Sidebar.Group className="pt-3 pb-0">
            <Sidebar.Menu>
              <Sidebar.MenuButton aria-label="Search" tooltip="Search ⌘K" onClick={search} icon={<Icon name="search" weight="light" />} className={SEARCH}>
                <span className="flex-1">Search…</span>
                <kbd className="rounded-sm border border-kumo-line px-1 font-mono text-xs">⌘K</kbd>
              </Sidebar.MenuButton>
            </Sidebar.Menu>
          </Sidebar.Group>
          <nav ref={nav} aria-label="Main" className="flex flex-col py-1">
            {NAV_GROUPS.map(([group, items]) => (
              <Sidebar.Group key={group} role="group" aria-label={group}>
                <Sidebar.GroupLabel aria-hidden="true" className="text-xs font-medium">{group}</Sidebar.GroupLabel>
                <Sidebar.Menu>
                  {items.map(([to, label, icon]) => (
                    <Sidebar.MenuButton key={to} href={to} active={active === to} tooltip={label} icon={<Icon name={icon} weight={active === to ? "fill" : "light"} />} aria-current={active === to ? "page" : undefined} className={NAV_ITEM}>
                      {label}
                      {to === "/incidents" && signals.incidents > 0 && <Badge variant="red" className="ml-auto">{signals.incidents}</Badge>}
                      {to === "/servers" && signals.offline > 0 && <span className="ml-auto size-2 rounded-full bg-kumo-danger" title={offlineText(signals.offline)}><span className="sr-only">{offlineText(signals.offline)}</span></span>}
                    </Sidebar.MenuButton>
                  ))}
                </Sidebar.Menu>
              </Sidebar.Group>
            ))}
          </nav>
        </Sidebar.Content>
        <Sidebar.Footer className="h-auto py-2">
          <div className="flex w-full items-center gap-1 group-data-[state=collapsed]/sidebar:flex-col">
            <Link to="/account" aria-label="Account" aria-current={active === "/account" ? "page" : undefined}
              className="flex min-w-0 flex-1 items-center gap-2.5 rounded-lg px-2 py-1.5 hover:bg-kumo-tint aria-[current=page]:bg-kumo-tint group-data-[state=collapsed]/sidebar:flex-none group-data-[state=collapsed]/sidebar:p-1">
              <span className="grid size-7 shrink-0 place-items-center rounded-sm bg-kumo-recessed text-xs font-semibold uppercase ring ring-kumo-line" aria-hidden="true">{(session.email ?? "?").charAt(0)}</span>
              <span className={cn("min-w-0", WIDE_ONLY)}>
                <span className="block truncate text-sm font-medium">Account</span>
                {session.email && <span className="block truncate text-xs text-kumo-subtle">{session.email}</span>}
              </span>
            </Link>
            <Button variant="ghost" size="sm" shape="square" aria-label={theme === "dark" ? "Light theme" : "Dark theme"} title={theme === "dark" ? "Light theme" : "Dark theme"} onClick={flip}><Icon name={theme === "dark" ? "sun" : "theme"} /></Button>
            <Sidebar.Trigger className="hidden md:flex" />
          </div>
        </Sidebar.Footer>
      </Sidebar>
      <div className="flex h-full min-w-0 flex-1 flex-col">
        <header className="sticky top-0 z-20 flex h-14 shrink-0 items-center gap-1 border-b border-kumo-line bg-kumo-canvas px-3">
          <Sidebar.Trigger className="md:hidden" />
          <nav className="flex min-w-0 flex-1 items-center gap-2 overflow-hidden whitespace-nowrap px-2 text-kumo-subtle" aria-label="Breadcrumb">
            {crumb.map((c, i) => (
              <span key={i} className="flex items-center gap-2">
                {i > 0 && <span aria-hidden="true">/</span>}
                {i === crumb.length - 1 ? <b className="font-medium text-kumo-default">{c}</b> : c}
              </span>
            ))}
          </nav>
          <Button variant="ghost" shape="square" size="sm" className="md:hidden" aria-label="Search" title="Search" onClick={search}><Icon name="search" /></Button>
        </header>
        <main className="relative min-w-0 flex-1 overflow-auto">
          <div className="flex max-w-[var(--w-wide)] flex-col gap-6 px-4 pt-5 pb-24 md:px-8 md:pt-7">
            {session.adopting && (
              <Banner variant="alert" role="status" description="This panel was restored from a backup. It changes nothing until you review what the servers run."
                action={<ButtonLink to="/adopt" size="sm">Review</ButtonLink>} />
            )}
            {session.managed && <BillingLine />}
            {children}
          </div>
        </main>
      </div>
      {finding && <Palette pages={PAGES} onClose={() => setFinding(false)} />}
    </Sidebar.Provider>
    </Toasty>
    </LinkProvider>
  );
}

type Billing = { state: string; trial_ends?: number; deploys: boolean; card_url?: string };

function BillingLine() {
  const b = useLoad<Billing>("/api/billing").data;
  if (!b?.card_url) return null;
  const card = (label: string) => <ButtonLink to={b.card_url!} size="sm">{label}</ButtonLink>;
  if (!b.deploys) {
    return <Banner variant="alert" role="status" description="New deploys are paused until this panel's subscription is paid; running apps keep running." action={card("Add a card")} />;
  }
  if (b.state === "past_due") {
    return <Banner variant="alert" role="status" description="Your last payment did not go through." action={card("Update the card")} />;
  }
  if (b.state === "trialing" && b.trial_ends) {
    const days = Math.max(0, Math.ceil((b.trial_ends - Date.now()) / 864e5));
    return <Banner variant="secondary" role="status" description={`Trial: ${days} ${days === 1 ? "day" : "days"} left.`} action={card("Add a card")} />;
  }
  return null;
}
