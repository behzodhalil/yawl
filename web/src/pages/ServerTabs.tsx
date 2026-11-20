import { Link } from "../ui";

const TABS: [string, string][] = [["", "Overview"], ["firewall", "Firewall"], ["disk", "Disk"], ["os", "OS updates"], ["drain", "Log drain"], ["tailscale", "Tailscale"]];

export function ServerTabs({ name, active }: { name: string; active: string }) {
  return (
    <div className="tabs">
      {TABS.map(([k, label]) => (
        <Link key={k} to={`/servers/${encodeURIComponent(name)}${k ? "/" + k : ""}`} className={k === active ? "on" : ""}>{label}</Link>
      ))}
    </div>
  );
}
