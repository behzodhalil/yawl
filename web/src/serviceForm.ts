import type { InspectData, ServiceForm, Volume } from "./types";

export const DB_VERSIONS: Record<string, number[]> = { postgres: [17, 18, 16], valkey: [8], mysql: [8], mongodb: [8], mariadb: [11], clickhouse: [25] };
export const DB_SHOWN: Record<string, string> = { mysql: "8.4", mongodb: "8.0", mariadb: "11.4", clickhouse: "25.8" };
export const ALWAYS_BACKED_UP = ["postgres", "mysql", "mongodb", "mariadb"];
export const isDb = (kind: string) => kind in DB_VERSIONS;

export function emptyForm(kind: string): ServiceForm {
  const db = isDb(kind);
  return {
    name: "", kind, repo: "", branch: db ? "" : "main", port: kind === "web" ? 10000 : 0, health_path: db || kind === "cron" ? "" : "/",
    health_timeout: 0, drain_timeout: 0, auto_deploy: true, release: [], release_timeout: 0, domains: [],
    version: db ? DB_VERSIONS[kind][0] : 0, backup_at: db ? "03:00" : "", outside_port: 0, allow: [],
    schedule: "", timezone: "", command: [], timeout: kind === "cron" ? 3600 : 0, volumes: [], cpu: 0, memory: "", image: "",
    backup: ALWAYS_BACKED_UP.includes(kind),
  };
}

export function volumeError(v: Volume): string {
  const name = v.name.trim();
  const path = v.path.trim();
  if (!name && !path) return "";
  if (!name) return "Name the volume.";
  if (!path.startsWith("/")) return "The path starts with /, like /app/uploads.";
  return "";
}

export const releaseLines = (text: string) => text.split("\n").map((l) => l.trim()).filter(Boolean);

export function nameFromRepo(repo: string): string {
  const last = repo.replace(/\.git$/, "").replace(/\/+$/, "").split(/[/:]/).pop() ?? "";
  return last.toLowerCase().replace(/[^a-z0-9-]+/g, "-").replace(/^[^a-z]+/, "").slice(0, 31).replace(/-+$/, "");
}

export function planLines(d: InspectData, kind: string): string[] {
  const out: string[] = [];
  if (d.builder) out.push(`Made with ${d.builder}`);
  if (d.dockerfile) out.push("Dockerfile found");
  else if (d.static) out.push("Vite site: built, then served as static files");
  else if (d.language) out.push(`No Dockerfile: Yawl will build it as ${d.language}`);
  else out.push("No Dockerfile: Railpack will build it");
  if (kind === "web") {
    if (d.reads_port) out.push("listens on PORT");
    else if (d.port) out.push(`listens on ${d.port}`);
    else out.push("does not read PORT: set the port it listens on below");
    out.push(`health path ${d.health_path}`);
  }
  const n = d.missing.length;
  out.push(n === 0 ? "no keys missing" : `${n} key${n === 1 ? "" : "s"} missing: ${d.missing.join(", ")}`);
  return out;
}

const KEY_KINDS: [RegExp, string[]][] = [[/REDIS|VALKEY/, ["valkey"]], [/MYSQL|MARIADB/, ["mysql", "mariadb"]], [/MONGO/, ["mongodb"]], [/CLICKHOUSE/, ["clickhouse"]],
  [/POSTGRES|^PG/, ["postgres"]], [/DATABASE/, ["postgres", "mysql", "mariadb", "mongodb"]]];

export function dbOptions(key: string, services: { service: string; kind: string }[]): string[] {
  const kinds = KEY_KINDS.find(([re]) => re.test(key))?.[1] ?? [];
  return services.filter((s) => kinds.includes(s.kind)).map((s) => s.service);
}
