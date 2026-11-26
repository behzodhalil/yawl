import type { ServerView } from "../types";
import { NavTabs, POLL_MS, useLoad } from "../ui";
import { ServerState } from "./Servers";

const TABS: [string, string][] = [["", "Overview"], ["firewall", "Firewall"], ["disk", "Disk"], ["os", "OS updates"], ["drain", "Log drain"], ["tailscale", "Tailscale"]];

export function ServerTabs({ name, active, server }: { name: string; active: string; server?: ServerView }) {
  const list = useLoad<ServerView[]>(server ? null : "/api/servers", POLL_MS);
  const s = server ?? list.data?.find((x) => x.name === name);
  return (
    <>
      <div className="flex flex-wrap items-center gap-3"><h1>{name}</h1>{s && <ServerState s={s} />}</div>
      <NavTabs label="Server" tabs={TABS.map(([k, label]) => ({ to: `/servers/${encodeURIComponent(name)}${k ? "/" + k : ""}`, label, current: k === active }))} />
    </>
  );
}
