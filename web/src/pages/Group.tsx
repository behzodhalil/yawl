import { useContext, type ReactNode } from "react";
import { LayerCard, cn } from "@cloudflare/kumo";
import { SessionContext } from "../session";
import { Shell } from "../Shell";
import { ButtonLink, Icon, Link, NavTabs, useLoad } from "../ui";
import { Telegram } from "./settings/Telegram";
import { Notifications } from "./settings/Notifications";
import { Watcher } from "./settings/Watcher";
import { Storage } from "./settings/Storage";
import { GitHub } from "./settings/GitHub";
import { GitLab } from "./settings/GitLab";
import { Gitea } from "./settings/Gitea";
import { Builds } from "./settings/Builds";
import { Tokens } from "./settings/Tokens";
import { Registries } from "./settings/Registries";
import { SharedVars } from "./settings/SharedVars";
import { SSO } from "./settings/SSO";
import { Recovery } from "./settings/Recovery";
import { Account } from "./settings/Account";
import { Panel } from "./settings/Panel";
import { Updates } from "./settings/Updates";
import { Trash } from "./settings/Trash";
import { Audit } from "./settings/Audit";
import { Members } from "./settings/Members";
import { Appearance } from "./settings/Appearance";
import { Agents } from "./Agents";

type Section = [string, string, () => ReactNode];

export const GROUPS: Record<string, { title: string; sections: Section[] }> = {
  integrations: { title: "Integrations", sections: [
    ["github", "GitHub", () => <GitHub />],
    ["gitlab", "GitLab", () => <GitLab />],
    ["gitea", "Gitea and Forgejo", () => <Gitea />],
    ["registries", "Registries", () => <Registries />],
    ["builds", "Builds", () => <Builds />],
    ["agents", "AI agents", () => <Agents />],
  ] },
  notifications: { title: "Notifications", sections: [
    ["telegram", "Telegram", () => <Telegram />],
    ["slack", "Slack", () => <Notifications only="slack" />],
    ["discord", "Discord", () => <Notifications only="discord" />],
    ["email", "Email", () => <Notifications only="email" />],
    ["webhook", "Webhook", () => <Notifications only="webhook" />],
    ["ntfy", "ntfy", () => <Notifications only="ntfy" />],
    ["watcher", "Watcher", () => <Watcher />],
  ] },
  team: { title: "Team", sections: [
    ["members", "Members", () => <Members />],
    ["sso", "Sign-in", () => <SSO />],
    ["tokens", "API tokens", () => <Tokens />],
  ] },
  activity: { title: "Activity", sections: [
    ["audit", "Audit log", () => <Audit />],
    ["trash", "Trash", () => <Trash />],
  ] },
  settings: { title: "Settings", sections: [
    ["panel", "Panel", () => <Panel />],
    ["storage", "Backup storage", () => <Storage />],
    ["updates", "Updates", () => <Updates />],
    ["recovery", "Recovery key", () => <Recovery />],
    ["appearance", "Appearance", () => <Appearance />],
  ] },
};

const CELL_SETTINGS = ["storage", "recovery", "appearance"];

const SECTION_ALIAS: Record<string, string> = { "sign-in": "sso" };

const NOT_CONNECTED = "Not connected";

export function Group({ group, section }: { group: string; section: string }) {
  const managed = useContext(SessionContext).managed;
  const all = GROUPS[group];
  const g = managed && group === "settings" ? { ...all, sections: all.sections.filter((s) => CELL_SETTINGS.includes(s[0])) } : all;
  if (group === "integrations" || group === "notifications") return <Listed group={group} section={section} />;
  const cur = g.sections.find((s) => s[0] === (SECTION_ALIAS[section] ?? section)) ?? g.sections[0];
  return (
    <Shell crumb={[g.title, cur[1]]} active={`/${group}`}>
      <h1>{g.title}</h1>
      <NavTabs label="Tabs" tabs={g.sections.map(([k, label]) => ({ to: `/${group}/${k}`, label, current: k === cur[0] }))} />
      {cur[2]()}
    </Shell>
  );
}

function Listed({ group, section }: { group: "integrations" | "notifications"; section: string }) {
  const g = GROUPS[group];
  const cur = g.sections.find((s) => s[0] === section);
  if (cur) {
    return (
      <Shell crumb={[<Link to={`/${group}`}>{g.title}</Link>, cur[1]]} active={`/${group}`}>
        <h1>{cur[1]}</h1>
        {cur[2]()}
      </Shell>
    );
  }
  return <Shell crumb={[g.title]} active={`/${group}`}><h1>{g.title}</h1>{group === "integrations" ? <IntegrationList /> : <ChannelList />}</Shell>;
}

type Line = { k: string; name: string; what: string; state: string; ok: boolean; act: string; icon?: string };

function Lines({ group, rows }: { group: string; rows: Line[] }) {
  return (
    <LayerCard render={<ul aria-label={GROUPS[group].title} />} className="divide-y divide-kumo-hairline p-0">
      {rows.map((r) => (
        <li key={r.k} className="grid grid-cols-[28px_minmax(0,1fr)_auto] items-center gap-3.5 px-4.5 py-3.5 md:grid-cols-[28px_minmax(0,1fr)_200px_140px]">
          <span className="grid size-7 place-items-center rounded-md border border-kumo-hairline [&_.ico]:size-4.5"><Icon name={r.icon ?? r.k} /></span>
          <div><b className="font-medium">{r.name}</b><div className="text-sm text-kumo-subtle">{r.what}</div></div>
          <span className={cn("hidden text-sm md:block", r.ok ? "text-kumo-success" : "text-kumo-subtle")}>{r.state}</span>
          <div className="flex justify-end"><ButtonLink to={`/${group}/${r.k}`} variant="secondary">{r.act}</ButtonLink></div>
        </li>
      ))}
    </LayerCard>
  );
}

type Hook = { set: boolean; host: string };
type Channels = { slack: Hook; discord: Hook; webhook?: Hook; ntfy?: Hook; email: { set: boolean; to: string } };

function ChannelList() {
  const tg = useLoad<{ state: string; relay?: { chats: { gone_at: number | null }[] } }>("/api/settings/telegram");
  const ch = useLoad<Channels>("/api/settings/notifications");
  const watch = useLoad<{ state: string }>("/api/settings/watch");
  const hook = (k: string, name: string, what: string, h?: Hook): Line =>
    ({ k, name, what, state: !ch.data ? "" : h?.set ? `Posts to ${h.host}` : NOT_CONNECTED, ok: !!h?.set, act: h?.set ? "Manage" : "Set up" });
  const t = tg.data, w = watch.data, c = ch.data;
  const chats = t?.relay?.chats ?? [], gone = chats.filter((x) => x.gone_at).length;
  const tgOK = chats.length ? !gone : t?.state === "linked";
  const rows: Line[] = [
    { k: "telegram", name: "Telegram", what: "Alerts in a Telegram chat or group.", ok: tgOK, act: chats.length || tgOK ? "Manage" : "Set up",
      state: !t ? "" : gone ? `${plural(gone, "chat")} stopped` : chats.length ? `Connected, ${plural(chats.length, "chat")}` : tgOK ? "Connected" : NOT_CONNECTED },
    hook("slack", "Slack", "An incoming webhook in a channel.", c?.slack),
    hook("discord", "Discord", "A channel's webhook.", c?.discord),
    { k: "email", name: "Email", what: "Through your own SMTP server.", icon: "mail", ok: !!c?.email.set, act: c?.email.set ? "Manage" : "Set up",
      state: !c ? "" : c.email.set ? `To ${c.email.to}` : NOT_CONNECTED },
    hook("webhook", "Webhook", "Every alert POSTed as JSON to a URL.", c?.webhook),
    hook("ntfy", "ntfy", "A topic on ntfy.sh or your own server.", c?.ntfy),
    { k: "watcher", name: "Watcher", what: "Checks from outside, so an outage of this server is heard too.", icon: "globe", ok: w?.state === "on", act: w?.state === "on" ? "Manage" : "Set up",
      state: !w ? "" : w.state === "on" ? "Watching" : w.state === "error" ? "Failed" : NOT_CONNECTED },
  ];
  return <Lines group="notifications" rows={rows} />;
}

const plural = (n: number, one: string) => `${n} ${one}${n === 1 ? "" : "s"}`;

function IntegrationList() {
  const github = useLoad<{ connected: boolean; slug?: string }>("/api/github");
  const gitlab = useLoad<{ host: string }[]>("/api/settings/gitlab");
  const gitea = useLoad<{ host: string }[]>("/api/settings/gitea");
  const regs = useLoad<unknown[]>("/api/registries");
  const builds = useLoad<{ server: string }>("/api/settings/builds");
  const hosts = (k: string, name: string, what: string, l?: { host: string }[]): Line =>
    ({ k, name, what, state: !l ? "" : l.length ? `Connected: ${l.map((h) => h.host).join(", ")}` : NOT_CONNECTED, ok: !!l?.length, act: l?.length ? "Manage" : "Connect" });
  const gh = github.data, r = regs.data, b = builds.data;
  return <Lines group="integrations" rows={[
    { k: "github", name: "GitHub", what: "Deploys from your repositories through this panel's own GitHub App.", ok: !!gh?.connected,
      state: !gh ? "" : gh.connected ? `Connected: ${gh.slug}` : NOT_CONNECTED, act: gh?.connected ? "Manage" : "Connect" },
    hosts("gitlab", "GitLab", "Deploys from gitlab.com or your own GitLab, with a token.", gitlab.data),
    hosts("gitea", "Gitea and Forgejo", "Deploys from a Gitea or Forgejo you run.", gitea.data),
    { k: "registries", name: "Registries", what: "Logins for private images.", icon: "docker", ok: false,
      state: !r ? "" : r.length ? plural(r.length, "login") : "No logins yet", act: r?.length ? "Manage" : "Add a login" },
    { k: "builds", name: "Builds", what: "Where images are built before they ship.", icon: "build", ok: false,
      state: !b ? "" : b.server ? `On ${b.server}` : "Each server builds its own", act: "Change" },
    { k: "agents", name: "AI agents", what: "Claude Code, Cursor or Codex deploy and debug with your token's role.", icon: "code", ok: false,
      state: "MCP server and agent skill", act: "Set up" },
  ]} />;
}

export function SharedPage() {
  return <Shell crumb={["Shared variables"]} active="/shared"><h1>Shared variables</h1><SharedVars /></Shell>;
}

export function AccountPage() {
  return <Shell crumb={["Account"]} active="/account"><h1>Account</h1><Account /></Shell>;
}
