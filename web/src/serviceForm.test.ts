import { describe, expect, it } from "vitest";
import { dbOptions, emptyForm, nameFromRepo, planLines, releaseLines, volumeError } from "./serviceForm";
import type { InspectData } from "./types";

const base: InspectData = { commit: "abc", dockerfile: true, language: "", reads_port: true, port: 0, health_path: "/health", missing: [], warnings: [], builder: "", static: false, notes: [] };

describe("planLines", () => {
  it("an app made with Lovable, served as files", () => {
    expect(planLines({ ...base, dockerfile: false, language: "node 22", builder: "Lovable", static: true, health_path: "/" }, "web"))
      .toEqual(["Made with Lovable", "Vite site: built, then served as static files", "listens on PORT", "health path /", "no keys missing"]);
  });
  it("a Dockerfile repo", () => {
    expect(planLines(base, "web")).toEqual(["Dockerfile found", "listens on PORT", "health path /health", "no keys missing"]);
  });
  it("a detected Node repo with three missing keys", () => {
    const d = { ...base, dockerfile: false, language: "node 22", missing: ["DATABASE_URL", "SECRET_KEY", "SENTRY_DSN"] };
    expect(planLines(d, "web")).toEqual([
      "No Dockerfile: Yawl will build it as node 22", "listens on PORT", "health path /health", "3 keys missing: DATABASE_URL, SECRET_KEY, SENTRY_DSN",
    ]);
  });
  it("a fixed port, one key, and a worker", () => {
    expect(planLines({ ...base, reads_port: false, port: 8080, missing: ["A"] }, "web")[1]).toBe("listens on 8080");
    expect(planLines({ ...base, missing: ["A"] }, "web")[3]).toBe("1 key missing: A");
    expect(planLines({ ...base, reads_port: false }, "web")[1]).toBe("does not read PORT: set the port it listens on below");
    expect(planLines(base, "worker")).toEqual(["Dockerfile found", "no keys missing"]);
  });
  it("no Dockerfile and no language", () => {
    expect(planLines({ ...base, dockerfile: false, language: "" }, "web")[0]).toBe(
      "No Dockerfile: Railpack will build it");
  });
});

describe("form helpers", () => {
  it("releaseLines drops empty lines and trims", () => expect(releaseLines("a\n\n b \n")).toEqual(["a", "b"]));
  it("emptyForm defaults", () => {
    expect(emptyForm("web")).toMatchObject({ kind: "web", branch: "main", port: 10000, health_path: "/", auto_deploy: true, release: [], domains: [], allow: [] });
    expect(emptyForm("postgres")).toMatchObject({ kind: "postgres", version: 17, backup_at: "03:00" });
  });
  it("nameFromRepo", () => {
    expect(nameFromRepo("https://github.com/acme/My_API.git")).toBe("my-api");
    expect(nameFromRepo("git@github.com:acme/web")).toBe("web");
    expect(nameFromRepo("")).toBe("");
  });
});

describe("cron form", () => {
  it("starts empty with an hour's limit", () => {
    const f = emptyForm("cron");
    expect([f.branch, f.port, f.health_path, f.schedule, f.timezone, f.command, f.timeout]).toEqual(["main", 0, "", "", "", [], 3600]);
  });
});

describe("volumes", () => {
  it("asks for a name and a path from the root, and lets an empty row pass", () => {
    expect(volumeError({ name: "", path: "" })).toBe("");
    expect(volumeError({ name: " uploads ", path: " /app/uploads " })).toBe("");
    expect(volumeError({ name: "", path: "/app/uploads" })).toBe("Name the volume.");
    expect(volumeError({ name: "uploads", path: "app/uploads" })).toBe("The path starts with /, like /app/uploads.");
    expect(volumeError({ name: "uploads", path: "" })).toBe("The path starts with /, like /app/uploads.");
    expect(emptyForm("web").volumes).toEqual([]);
  });
});

describe("databases", () => {
  it("a Valkey form starts at version 8, backup off, 03:00", () => {
    expect(emptyForm("valkey")).toMatchObject({ kind: "valkey", version: 8, backup: false, backup_at: "03:00", branch: "", health_path: "" });
    expect(emptyForm("postgres")).toMatchObject({ version: 17, backup: true });
  });
  it("offers the databases a key would read", () => {
    const svcs = [{ service: "db", kind: "postgres" }, { service: "cache", kind: "valkey" }, { service: "api", kind: "web" }];
    expect(dbOptions("REDIS_URL", svcs)).toEqual(["cache"]);
    expect(dbOptions("VALKEY_URL", svcs)).toEqual(["cache"]);
    expect(dbOptions("DATABASE_URL", svcs)).toEqual(["db"]);
    expect(dbOptions("STRIPE_KEY", svcs)).toEqual([]);
  });
  it("MySQL: version 8, always backed up; DATABASE_URL offers Postgres and MySQL", () => {
    expect(emptyForm("mysql")).toMatchObject({ kind: "mysql", version: 8, backup: true });
    const svcs = [{ service: "db", kind: "postgres" }, { service: "shop-db", kind: "mysql" }];
    expect(dbOptions("DATABASE_URL", svcs)).toEqual(["db", "shop-db"]);
    expect(dbOptions("MYSQL_URL", svcs)).toEqual(["shop-db"]);
  });
  it("MongoDB: version 8, always backed up; MONGODB_URI and DATABASE_URL offer it", () => {
    expect(emptyForm("mongodb")).toMatchObject({ kind: "mongodb", version: 8, backup: true });
    const svcs = [{ service: "db", kind: "postgres" }, { service: "docs", kind: "mongodb" }];
    expect(dbOptions("MONGODB_URI", svcs)).toEqual(["docs"]);
    expect(dbOptions("MONGO_URL", svcs)).toEqual(["docs"]);
    expect(dbOptions("DATABASE_URL", svcs)).toEqual(["db", "docs"]);
  });

  it("MariaDB and ClickHouse", () => {
    expect(emptyForm("mariadb")).toMatchObject({ kind: "mariadb", version: 11, backup: true });
    expect(emptyForm("clickhouse")).toMatchObject({ kind: "clickhouse", version: 25, backup: false });
    const svcs = [{ service: "db", kind: "postgres" }, { service: "shop", kind: "mariadb" }, { service: "events", kind: "clickhouse" }];
    expect(dbOptions("MYSQL_URL", svcs)).toEqual(["shop"]);
    expect(dbOptions("MARIADB_URL", svcs)).toEqual(["shop"]);
    expect(dbOptions("DATABASE_URL", svcs)).toEqual(["db", "shop"]);
    expect(dbOptions("CLICKHOUSE_URL", svcs)).toEqual(["events"]);
  });
});
