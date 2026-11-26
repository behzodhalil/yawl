import { Firewall } from "./Firewall";
import { Disk } from "./Disk";
import { Tailscale } from "./Tailscale";
import { OSUpdates } from "./OSUpdates";
import { Drain } from "./Drain";
import { createElement as h } from "react";
import { PAGES } from "../registry";
import { Projects } from "./Projects";
import { NewProject } from "./NewProject";
import { Templates } from "./Templates";
import { Project } from "./Project";
import { Servers } from "./Servers";
import { AddServer } from "./AddServer";
import { NewService } from "./NewService";
import { ServiceFrame } from "./service/Service";
import { Deploy } from "./service/Deploy";
import { AccountPage, Group, SharedPage } from "./Group";
import { ServerOverview } from "./ServerOverview";
import { Adopt } from "./Adopt";
import { CliLogin } from "./CliLogin";
import { Move } from "./Move";
import { Incidents } from "./Incidents";
import { StatusPage, StatusPages } from "./StatusPages";

PAGES["projects"] = () => h(Projects);
PAGES["new-project"] = () => h(NewProject);
PAGES["project"] = (r) => h(Project, { project: r.params.project, env: r.params.env, key: r.params.project + r.params.env });
PAGES["pulls"] = (r) => h(Project, { project: r.params.project, env: r.params.env, pulls: true, key: r.params.project + r.params.env + "/pulls" });
PAGES["servers"] = () => h(Servers);
PAGES["add-server"] = () => h(AddServer);
PAGES["firewall"] = (r) => h(Firewall, { name: r.params.name, key: r.params.name });
PAGES["disk"] = (r) => h(Disk, { name: r.params.name, key: r.params.name });
PAGES["tailscale"] = (r) => h(Tailscale, { name: r.params.name, key: r.params.name });
PAGES["os"] = (r) => h(OSUpdates, { name: r.params.name, key: r.params.name });
PAGES["drain"] = (r) => h(Drain, { name: r.params.name, key: r.params.name });
PAGES["new-service"] = (r) => h(NewService, { project: r.params.project, env: r.params.env, kind: r.params.kind, key: r.params.project + r.params.env + r.params.kind });
PAGES["templates"] = (r) => h(Templates, { project: r.params.project, env: r.params.env, key: r.params.project + r.params.env });
PAGES["service"] = (r) => {
  const p = { project: r.params.project, env: r.params.env, service: r.params.service };
  return h(ServiceFrame, { p, tab: r.params.tab, key: p.project + p.env + p.service });
};
PAGES["deploy"] = (r) => {
  const p = { project: r.params.project, env: r.params.env, service: r.params.service };
  return h(ServiceFrame, { p, tab: "events", key: p.project + p.env + p.service }, h(Deploy, { ...p, id: r.params.id, key: r.params.id }));
};
for (const g of ["integrations", "notifications", "team", "activity", "settings"]) {
  PAGES[g] = (r) => h(Group, { group: g, section: r.params.section, key: g + r.params.section });
}
PAGES["shared"] = () => h(SharedPage, {});
PAGES["account"] = () => h(AccountPage, {});
PAGES["server"] = (r) => h(ServerOverview, { name: r.params.name, key: r.params.name });
PAGES["adopt"] = () => h(Adopt);
PAGES["cli-login"] = () => h(CliLogin);
PAGES["move"] = (r) => h(Move, { project: r.params.project, env: r.params.env, key: r.params.project + r.params.env });
PAGES["incidents"] = () => h(Incidents);
PAGES["status-pages"] = () => h(StatusPages);
PAGES["status-page"] = (r) => h(StatusPage, { id: r.params.id, key: r.params.id });
