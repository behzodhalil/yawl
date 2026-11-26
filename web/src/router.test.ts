import { describe, expect, it } from "vitest";
import { route } from "./router";

describe("route", () => {
  const cases: [string, string, Record<string, string>][] = [
    ["/claim", "claim", {}],
    ["/reset", "reset", {}],
    ["/sign-in", "sign-in", {}],
    ["/", "projects", {}],
    ["/projects/new", "new-project", {}],
    ["/p/acme/production", "project", { project: "acme", env: "production" }],
    ["/p/acme/staging/new", "new-service", { project: "acme", env: "staging", kind: "" }],
    ["/p/acme/production/s/api", "service", { project: "acme", env: "production", service: "api", tab: "" }],
    ["/p/acme/production/s/api/logs", "service", { project: "acme", env: "production", service: "api", tab: "logs" }],
    ["/p/acme/production/s/api/deploys/dep_1", "deploy", { project: "acme", env: "production", service: "api", id: "dep_1" }],
    ["/servers", "servers", {}],
    ["/servers/new", "add-server", {}],
    ["/settings/telegram", "settings", { section: "telegram" }],
    ["/settings", "settings", { section: "" }],
    ["/integrations/gitea", "integrations", { section: "gitea" }],
    ["/notifications", "notifications", { section: "" }],
    ["/team/sso", "team", { section: "sso" }],
    ["/activity/trash", "activity", { section: "trash" }],
    ["/shared", "shared", {}],
    ["/account", "account", {}],
    ["/servers/hel1", "server", { name: "hel1" }],
  ];
  for (const [path, page, params] of cases) {
    it(path, () => expect(route(path)).toEqual({ page, params }));
  }
  it("an unknown path", () => expect(route("/what/ever").page).toBe("not-found"));
  it("ignores a trailing slash", () => expect(route("/servers/").page).toBe("servers"));
});
