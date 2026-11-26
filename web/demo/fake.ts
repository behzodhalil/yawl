import grafanaIcon from "../../internal/server/templates/icons/grafana.svg?raw";
import lokiIcon from "../../internal/server/templates/icons/grafana-loki.svg?raw";
import n8nIcon from "../../internal/server/templates/icons/n8n.svg?raw";
import umamiIcon from "../../internal/server/templates/icons/umami.svg?raw";
import uptimeIcon from "../../internal/server/templates/icons/uptime-kuma.svg?raw";
import pocketbaseIcon from "../../internal/server/templates/icons/pocketbase.svg?raw";
import appwriteIcon from "./icons/appwrite.svg?raw";
import supabaseIcon from "./icons/supabase.svg?raw";
import sodium from "libsodium-wrappers";
import type { DeployView, DiffRow, ServiceForm, StatusRow } from "../src/types";
import { isDb } from "../src/serviceForm";

const V = () => sodium.base64_variants.ORIGINAL;
const b64 = (b: Uint8Array) => sodium.to_base64(b, V());
const B32 = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567";
function base32(b: Uint8Array): string {
  let out = "", bits = 0, acc = 0;
  for (const x of b) {
    acc = (acc << 8) | x;
    bits += 8;
    while (bits >= 5) out += B32[(acc >>> (bits -= 5)) & 31];
  }
  if (bits > 0) out += B32[(acc << (5 - bits)) & 31];
  return out;
}

type Keys = { pub: Uint8Array; priv: Uint8Array };
type Server = { id: string; name: string; ip: string; version: string; disk: number; keys: Keys;
  os: string; osId: string; kernel: string; cores: number; mem: number; diskTotal: number; use: number; booted: number };
type Deploy = { id: string; svc: string; commit: string; digest?: string; subject: string; by: string; rollback: boolean; created: number; fail?: string; restore?: boolean };
type Service = { id: string; project: string; env: string; form: ServiceForm; deploys: Deploy[]; public?: boolean; stopped?: boolean; maintenance?: boolean; passwordUser?: string; settingsAt?: number };
type Env = { project: string; name: string; server: string; preview?: boolean };
type Var = { project: string; env: string; service: string; key: string; agentBox?: string; recoveryBox?: string; ref?: string; build: boolean; updated: number };
type Backup = { id: string; svc: string; created: number; size: number; volume?: string };
type Move = { id: string; project: string; env: string; from: string; to: string; started: number; cause: string; failed: boolean };

const now = () => Date.now();
const min = 60_000, hour = 60 * min, day = 24 * hour;
const id = (p: string) => p + Math.random().toString(36).slice(2, 14);
const commits = ["9f2c41e7b0d3", "4be81c0a77f2", "c03d9e52a1b4", "71aa6f30de95"];
const subjects = ["Show totals on the invoice list", "Add PDF export to invoices", "Fix invoice rounding", "Bump dependencies"];
const pushers = ["dilnoza", "owner", "dilnoza", "owner"];
const dashed = (ip: string) => ip.replaceAll(".", "-");
const STEP_MS = 1400;

let recovery: Keys;
export let recoveryText = "";
const servers: Server[] = [];
const envs: Env[] = [];
const services: Service[] = [];
const vars: Var[] = [];
const backups: Backup[] = [];
let gitlabHosts: string[] = [];
let giteaHosts: string[] = [];
const registries: { host: string; username: string }[] = [{ host: "ghcr.io", username: "acme-bot" }];
let builds = { server: "", repository: "" };
let sso = { name: "", issuer: "", client_id: "", only: false, secret_set: false };
const shared: { scope: "team" | "server"; key: string; servers: string[]; build?: boolean; updated_at: number }[] = [];
const sources: Record<string, { provider: string; url?: string; path?: string; workspace?: string; env?: string; updated_at: number }[]> = {};
const drains: Record<string, { url: string; user: string }> = {};
const tailnet: Record<string, string> = {};
let hetzner = false;
const hetznerMade: { name: string; ip: string; at: number; late: boolean }[] = [];

const moves: Move[] = [];
const tokens = [{ id: "tok_laptop", name: "CLI on laptop", last_used_at: now() - 2 * hour, created_at: now() - 9 * day }];
let tg: { state: string; bot?: string; code?: string } = { state: "none" };
let tgPolls = 0;
type TgChat = { id: string; type: string; title: string; linked_at: number; gone_at: number | null };
type TgRoute = { servers: string[]; projects: string[] };
const relay: { chats: TgChat[]; routes: Record<string, TgRoute>; pending?: { code: string; link: string; expires_at: number; qr: string; bot: string }; polls: number; ended?: { why: string; title?: string } } =
  { chats: [], routes: {}, polls: 0 };
const demoChats: TgChat[] = [
  { id: "7", type: "private", title: "Behzod Halil", linked_at: 0, gone_at: null },
  { id: "-1001", type: "supergroup", title: "Acme ops", linked_at: 0, gone_at: null },
];
const demoQR = (text: string) => {
  let d = "", h = 7;
  for (let y = 0; y < 25; y++) for (let x = 0; x < 25; x++) {
    const finder = [[0, 0], [18, 0], [0, 18]].find(([fx, fy]) => x >= fx && x < fx + 7 && y >= fy && y < fy + 7);
    h = (h * 31 + text.charCodeAt((x + y * 25) % text.length)) % 9973;
    const on = finder ? !(x - finder[0] === 1 || x - finder[0] === 5 || y - finder[1] === 1 || y - finder[1] === 5) || (x - finder[0] >= 2 && x - finder[0] <= 4 && y - finder[1] >= 2 && y - finder[1] <= 4) : h % 2 === 0;
    if (on) d += `M${x + 4} ${y + 4}h1v1h-1z`;
  }
  return `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 33 33" shape-rendering="crispEdges"><rect width="33" height="33" fill="#fff"/><path d="${d}" fill="#000"/></svg>`;
};
const runsOf = new Map<string, { ID: string; ScheduledAt: number; StartedAt: number; FinishedAt: number; Exit: number; Cause: string; Output: string; Skipped: boolean; TimedOut: boolean }[]>();
const cronRuns = (s: Service) => {
  if (!runsOf.has(s.id)) {
    const day = 86_400_000;
    runsOf.set(s.id, [1, 2, 3].map((n) => ({ ID: `${s.id}_${n}`, ScheduledAt: now() - n * day, StartedAt: now() - n * day, FinishedAt: now() - n * day + 41_000,
      Exit: n === 2 ? 1 : 0, Cause: "", Output: n === 2 ? "Error: SMTP timed out" : "report sent to 14 people", Skipped: false, TimedOut: false })));
  }
  return runsOf.get(s.id)!;
};
const members: { id: string; email: string; role: string; last_sign_in_at?: number; two_step: boolean }[] = [
  { id: "own_1", email: "owner@acme.uz", role: "admin", last_sign_in_at: Date.now() - 3 * 60_000, two_step: true },
  { id: "own_2", email: "dilnoza@acme.uz", role: "developer", last_sign_in_at: Date.now() - 26 * 3_600_000, two_step: false },
];
const invites: { id: string; role: string; expires_at: number }[] = [];
const previews = [
  { id: "pre_1", number: 12, title: "Faster search", branch: "search", state: "live", source: "staging", env: "pr-12", comment: "Preview: https://api-pr-12.65-108-77-12.sslip.io (deployed 4be81c0)" },
  { id: "pre_2", number: 15, title: "Invoices in PDF", branch: "invoice-pdf", state: "waiting", source: "staging", comment: "" },
  { id: "pre_3", number: 42, title: "New checkout", branch: "checkout", state: "live", source: "production", env: "pr-42", comment: "Preview: https://web-pr-42.65-108-77-12.sslip.io (deployed a1b2c3d)" },
  { id: "pre_4", number: 44, title: "Dark invoices", branch: "dark-invoices", state: "making", source: "production", env: "pr-44", comment: "" },
];
type Trashed = { kind: string; id: string; label: string; deleted_by: string; deleted_at: number; purge_at: number; envs: Env[]; services: Service[] };
const trashed: Trashed[] = [];
const auditLog: { at: number; actor: string; text: string; kind: string; service?: string; container?: string }[] = [];
const audit = (text: string) => auditLog.unshift({ at: now(), actor: "owner@acme.uz", text, kind: "audit" });
function toTrash(kind: string, id: string, label: string, envMatch: (e: Env) => boolean, svcMatch: (s: Service) => boolean) {
  const t: Trashed = { kind, id, label, deleted_by: "owner", deleted_at: now(), purge_at: now() + 48 * 3_600_000, envs: [], services: [] };
  for (let i = envs.length - 1; i >= 0; i--) if (envMatch(envs[i])) t.envs.push(...envs.splice(i, 1));
  for (let i = services.length - 1; i >= 0; i--) if (svcMatch(services[i])) t.services.push(...services.splice(i, 1));
  trashed.unshift(t);
  audit(`deleted ${label}`);
}
const WATCH_URL = "https://yawl-watch-3f9a2c1d.acme.workers.dev";
let watchOn = false;
const heartbeats: { id: string; name: string; period_s: number; grace_s: number; url: string }[] = [];
const incidents: { id: string; subject: string; kind: string; label: string; source: string; opened_at: number; closed_at: number | null; cause: string; note: string }[] = [];
type StatusPage = { id: string; slug: string; title: string; server_id: string; domain: string; watcher: boolean; rows: { id: string; name: string; subject: string }[] };
type StatusIncident = { id: string; page_id: string; kind: string; title: string; rows: string[]; state: string; auto: boolean; starts_at: number; ends_at: number; resolved_at: number; created_at: number; updates: { id: string; at: number; body: string }[] };
const statusPages: StatusPage[] = [];
const statusIncidents: StatusIncident[] = [];
const DATA: Record<string, { columns: string[]; rows: (string | null)[][] }> = {
  users: { columns: ["id", "email", "name", "created_at"], rows: [
    ["1", "aziza@acme.uz", "Aziza Karimova", "2026-03-02 09:14:00+00"], ["2", "bobur@acme.uz", "Bobur Rashidov", "2026-04-18 16:40:12+00"],
    ["3", "nodira@mail.uz", null, "2026-09-07 11:02:55+00"], ["4", "timur@example.com", "Timur Aliev", "2026-11-20 08:31:09+00"]] },
  invoices: { columns: ["id", "user_id", "total", "paid_at"], rows: [
    ["1041", "1", "129.00", "2026-11-01 10:00:00+00"], ["1042", "2", "49.50", null], ["1043", "4", "310.00", "2026-11-22 14:12:40+00"]] },
  schema_migrations: { columns: ["version"], rows: [["20260301_init"], ["20260912_add_users_name"], ["20261027_add_invoices"]] },
};
let storage = { set: true, endpoint: "https://8f3a91c2d4e5b6a7.r2.cloudflarestorage.com", bucket: "acme-backups", region: "auto", access_key_end: "7Q2k" };
let panelDomain = "";

const server = (name: string) => servers.find((s) => s.name === name)!;
const envOf = (p: string, e: string) => envs.find((x) => x.project === p && x.name === e);
const svcOf = (p: string, e: string, s: string) => services.find((x) => x.project === p && x.env === e && x.form.name === s);
const seal = (k: Uint8Array, v: string) => b64(sodium.crypto_box_seal(new TextEncoder().encode(v), k));
const open = (k: Keys, box: string) => new TextDecoder().decode(sodium.crypto_box_seal_open(sodium.from_base64(box, V()), k.pub, k.priv));

function form(name: string, kind: string, over: Partial<ServiceForm> = {}): ServiceForm {
  return {
    name, kind, repo: kind === "postgres" ? "" : `https://github.com/acme/${name}`, branch: "main", port: kind === "postgres" ? 0 : 10000,
    health_path: kind === "postgres" ? "" : "/health", health_timeout: 60, drain_timeout: 30, auto_deploy: kind !== "postgres", release: [],
    release_timeout: 180, domains: [], version: kind === "postgres" ? 17 : 0, backup_at: kind === "postgres" ? "03:00" : "", outside_port: 0, allow: [],
    schedule: "", timezone: "", command: [], timeout: kind === "cron" ? 3600 : 0, volumes: [], cpu: 0, memory: "", image: "", backup: isDb(kind), ...over,
  };
}

function addService(project: string, env: string, f: ServiceForm, age: number): Service {
  const s: Service = { id: id("svc_"), project, env, form: f, deploys: [] };
  if (f.image) {
    [2, 1].forEach((n, i) => s.deploys.push({ id: id("dep_"), svc: s.id, commit: "", digest: "sha256:" + ["4f1c9a0b7e2d", "a83e55c01f9b"][i] + "0".repeat(52), subject: f.image, by: "owner", rollback: false, created: now() - age - n * day }));
  } else [3, 2, 1].forEach((n, i) => s.deploys.push({ id: id("dep_"), svc: s.id, commit: commits[i], subject: subjects[i], by: pushers[i], rollback: false, created: now() - age - n * day, ...(i === 1 && f.kind !== "postgres" ? { fail: "Health check on /health did not pass within 60s." } : {}) }));
  services.push(s);
  return s;
}

function setVar(project: string, env: string, service: string, key: string, value: string, build = false) {
  const e = envOf(project, env)!;
  vars.push({ project, env, service, key, build, updated: now() - 3 * day,
    agentBox: seal(server(e.server).keys.pub, value), recoveryBox: seal(recovery.pub, value) });
}

export async function boot() {
  await sodium.ready;
  const r = sodium.crypto_box_keypair();
  recovery = { pub: r.publicKey, priv: r.privateKey };
  recoveryText = "yawl-recovery-" + base32(r.privateKey);
  const mk = (name: string, ip: string, disk: number, m: Pick<Server, "os" | "osId" | "kernel" | "cores" | "mem" | "diskTotal" | "use" | "booted">) => {
    const k = sodium.crypto_box_keypair();
    servers.push({ id: id("srv_"), name, ip, version: "1.0.0", disk, keys: { pub: k.publicKey, priv: k.privateKey }, ...m });
  };
  mk("hel1", "198.51.100.10", 41, { os: "Ubuntu 24.04.1 LTS", osId: "ubuntu", kernel: "6.8.0-48-generic", cores: 4, mem: 8e9, diskTotal: 80e9, use: 0.46, booted: now() - 12 * day - 5 * hour });
  mk("fsn1", "198.51.100.11", 85, { os: "Debian GNU/Linux 12 (bookworm)", osId: "debian", kernel: "6.1.0-26-amd64", cores: 2, mem: 2e9, diskTotal: 40e9, use: 0.9, booted: now() - 3 * day - 2 * hour });
  relay.chats = demoChats.map((c) => ({ ...c, linked_at: now() - 2 * day }));
  relay.routes["-1001"] = { servers: [servers[1].id], projects: [] };
  for (const env of ["production", "staging"]) {
    envs.push({ project: "acme", name: env, server: "hel1", preview: env === "production" });
    addService("acme", env, form("web", "web", env === "production" ? { domains: ["acme.uz"] } : {}), env === "production" ? 0 : hour);
    addService("acme", env, form("api", "web", { release: ["/app/api", "migrate"], cpu: 0.5, memory: "512MB", ...(env === "production" ? { domains: ["api.acme.uz"], replicas: 2 } : {}) }), 0);
    addService("acme", env, form("db", "postgres"), 0);
    if (env === "production") addService("acme", env, form("report", "cron", { port: 0, health_path: "", schedule: "0 6 * * *", timezone: "Asia/Tashkent", command: ["/app/report", "--daily"] }), 0);
    if (env === "production") addService("acme", env, form("uptime", "web", { repo: "", branch: "", image: "louislam/uptime-kuma:1", port: 3001, auto_deploy: false, cpu: 0.25, memory: "256MB" }), 0);
    setVar("acme", env, "", "SENTRY_DSN", "https://4f1c@o221.ingest.sentry.io/77");
    setVar("acme", env, "api", "STRIPE_KEY", env === "production" ? "sk_live_51Hx9demo" : "sk_test_51Hx9demo");
    setVar("acme", env, "api", "SESSION_SECRET", "c2f8e1a94b7d06e3");
    setVar("acme", env, "db", "PASSWORD", env === "production" ? "Kp7wQm2xLr9v" : "Tz4nB8yHs1qe");
    setVar("acme", env, "db", "URL", `postgres://app:${env === "production" ? "Kp7wQm2xLr9v" : "Tz4nB8yHs1qe"}@db:5432/app`);
    vars.push({ project: "acme", env, service: "api", key: "DATABASE_URL", ref: "db.url", build: false, updated: now() - 3 * day });
  }
  setVar("acme", "production", "web", "PLAUSIBLE_DOMAIN", "acme.uz", true);
  vars[vars.length - 1].updated = now() - 10 * 60_000;
  shared.push({ scope: "team", key: "LOG_LEVEL", servers: servers.map((v) => v.name), updated_at: now() - 5 * day });
  envs.push({ project: "acme", name: "pr-12", server: "hel1" });
  addService("acme", "pr-12", form("api", "web", { branch: "search" }), hour);
  envs.push({ project: "blog", name: "production", server: "fsn1" });
  addService("blog", "production", form("site", "web", { repo: "https://github.com/acme/blog", volumes: [{ name: "uploads", path: "/app/uploads" }], memory: "512MB" }), 2 * day);
  const site = svcOf("blog", "production", "site")!;
  [3, 2, 1].forEach((n) => backups.push({ id: id("bak_"), svc: site.id, created: now() - n * day + 3.5 * hour, size: 212_000_000 + n * 3_400_000, volume: "uploads" }));
  const shell = (svc: Service, actor: string, ago: number, container: string) => auditLog.push({ at: now() - ago, actor, text: "terminal.opened", kind: "terminal.opened", service: svc.id, container });
  shell(svcOf("acme", "production", "api")!, "owner@acme.uz", 2 * hour, "yawl-acme-production-api-1");
  shell(svcOf("acme", "production", "api")!, "dilnoza@acme.uz", day + 3 * hour, "yawl-acme-production-api-1");
  shell(svcOf("acme", "production", "web")!, "owner@acme.uz", 4 * day, "yawl-acme-production-web-2");
  auditLog.push({ at: now() - 2 * 60_000, actor: "API token CLI on laptop (owner@acme.uz)", text: "deploy.queued", kind: "deploy.queued" },
    { at: now() - 3 * hour, actor: "API token codex-desktop (dilnoza@acme.uz)", text: "rolled back blog / production / site", kind: "deploy.rollback" },
    { at: now() - day, actor: "API token CLI on laptop (owner@acme.uz)", text: "spec.applied", kind: "spec.applied" });
  const db = svcOf("acme", "production", "db")!;
  [4, 3, 2, 1].forEach((n) => backups.push({ id: id("bak_"), svc: db.id, created: now() - n * day + 3 * hour, size: 38_000_000 + n * 1_200_000 }));
  statusPages.push({ id: "sp_acme", slug: "acme", title: "Acme status", server_id: servers[1].id, domain: "status.acme.uz", watcher: false, rows: [
    { id: "sr_web", name: "Website", subject: "service:" + svcOf("acme", "production", "web")!.id },
    { id: "sr_api", name: "API", subject: "service:" + svcOf("acme", "production", "api")!.id },
    { id: "sr_blog", name: "Blog", subject: "project:blog" }] });
  statusIncidents.push(
    { id: "si_1", page_id: "sp_acme", kind: "incident", title: "API is down", rows: ["sr_api"], state: "draft", auto: true, starts_at: 0, ends_at: 0, resolved_at: 0, created_at: now() - 25 * min,
      updates: [{ id: "su_1", at: now() - 25 * min, body: "We are looking into errors on the API." }] },
    { id: "si_2", page_id: "sp_acme", kind: "incident", title: "Slow checkout", rows: ["sr_web", "sr_api"], state: "published", auto: false, starts_at: 0, ends_at: 0, resolved_at: now() - 3 * day + 40 * min, created_at: now() - 3 * day,
      updates: [{ id: "su_2", at: now() - 3 * day, body: "Checkout takes up to 20 seconds. We are on it." }, { id: "su_3", at: now() - 3 * day + 40 * min, body: "A slow payment provider; checkout is fast again." }] });
}

const STEPS: Record<string, string[]> = {
  web: ["build", "start", "check", "switch", "drain"],
  release: ["build", "snapshot", "release", "start", "check", "switch", "drain"],
  postgres: ["ensure"],
  restore: ["restore", "ensure"],
  restore_volume: ["stop_old", "restore_volume", "start", "check", "switch"],
  cron: ["build", "schedule"],
};
const STATE_OF: Record<string, string> = { pull: "pulling", build: "building", snapshot: "snapshotting", release: "releasing", start: "starting",
  check: "checking", switch: "switching", drain: "draining", ensure: "starting", restore: "starting", schedule: "starting", stop_old: "stopping", restore_volume: "restoring" };
const OUTPUT: Record<string, string> = {
  build: "#1 [internal] load build definition from Dockerfile\n#2 [build 1/4] FROM docker.io/library/golang:1.23-alpine\n#5 [build 4/4] RUN go build -o /app ./cmd/app\n#7 exporting to image\n#7 naming to yawl/acme-api:latest done",
  release: "running migrate on the new image\napplied 20261027_add_invoices.sql\n1 migration in 0.4 s",
  check: "GET /health → 200 in 38 ms (3 in a row)",
  pull: "1: Pulling from louislam/uptime-kuma\nDigest: sha256:a83e55c01f9b\nStatus: Downloaded newer image for louislam/uptime-kuma:1",
  snapshot: "snapshot of db: 38.4 MB",
  restore: "restoring the backup into a new database: 38.4 MB",
};

function steps(d: Deploy, s: Service) {
  if (s.form.image && !d.restore) return ["pull", ...STEPS.web.slice(1)];
  return d.restore ? (s.form.kind === "postgres" ? STEPS.restore : STEPS.restore_volume) : s.form.kind === "postgres" ? STEPS.postgres : s.form.kind === "cron" ? STEPS.cron : s.form.release.length ? STEPS.release : STEPS.web;
}

function deployState(d: Deploy): { state: string; done: number; cause?: string } {
  const s = services.find((x) => x.id === d.svc)!;
  const list = steps(d, s);
  const n = Math.floor((now() - d.created) / STEP_MS) - 1;
  if (n < 0) return { state: "queued", done: 0 };
  const fi = list.includes("check") ? list.indexOf("check") : list.length - 1;
  if (d.fail && n >= fi) return { state: "failed", done: fi, cause: d.fail };
  if (n >= list.length) return { state: "live", done: list.length };
  return { state: STATE_OF[list[n]], done: n };
}

const TOOK: Record<string, number> = { pull: 14_000, build: 112_000, start: 3_000, check: 9_000, switch: 1_000, drain: 9_000 };
const stepMs = (d: Deploy, step: string) => Math.round((d.fail && step === "check" ? 61_000 : TOOK[step] ?? 4_000) * (0.6 + parseInt(d.commit.slice(0, 2) || "80", 16) / 400));

const rollbackTarget = (s: Service) => [...s.deploys].reverse().filter((d) => deployState(d).state !== "failed")[1];

function words(d: Deploy) {
  const s = services.find((x) => x.id === d.svc)!;
  const st = deployState(d), cur = current(s);
  const done = st.state === "live" || st.state === "failed";
  const ran = steps(d, s).slice(0, st.done + 1);
  return { subject: d.subject, by: d.by, digest: d.digest, took_ms: done ? ran.reduce((a, step) => a + stepMs(d, step), 0) : 0,
    live: d === cur, can_roll_back: st.state === "live" && d !== cur && (d.digest ? d.digest !== cur?.digest : d.commit !== cur?.commit), rollback_target: d === rollbackTarget(s) };
}

function deployView(d: Deploy): DeployView {
  const s = services.find((x) => x.id === d.svc)!;
  const st = deployState(d);
  const list = steps(d, s);
  let at = d.created;
  return {
    id: d.id, service: s.form.name, state: st.state, cause: st.cause, commit: d.commit, rollback: d.rollback, created_at: d.created, warnings: [], ...words(d),
    steps: list.slice(0, Math.min(list.length, st.done + 1)).map((step, i) => {
      const state = i < st.done ? "ok" : st.state === "failed" ? "failed" : st.state === "live" ? "ok" : "running";
      const started = at;
      at += stepMs(d, step);
      return { step, state, started_at: started, finished_at: state === "running" ? undefined : at,
        output: i < st.done || st.state === "failed" ? (st.state === "failed" && i === st.done ? FAILED : OUTPUT[step]) : undefined };
    }),
  };
}
const FAILED = "GET /health 500\nError: Cannot find module 'pdfkit'\n    at Module._resolveFilename (node:internal/modules/cjs/loader:1145:15)\n    at /app/src/invoices/pdf.js:3:13";

function current(s: Service): Deploy | undefined {
  return [...s.deploys].reverse().find((d) => deployState(d).state === "live") ?? s.deploys[s.deploys.length - 1];
}

function newDeploy(s: Service, commit = commits[3], rollback = false, extra: Partial<Deploy> = {}): Deploy {
  const d = { id: id("dep_"), svc: s.id, commit, subject: subjects[commits.indexOf(commit)] ?? "", by: "owner", rollback, created: now(), ...extra };
  s.deploys.push(d);
  return d;
}

function url(s: Service) {
  const e = envOf(s.project, s.env)!;
  return s.form.kind === "web" ? `https://${s.form.name}-${s.env}.${dashed(server(e.server).ip)}.sslip.io` : undefined;
}

function status(s: Service): StatusRow {
  const d = s.deploys[s.deploys.length - 1];
  const st = d ? deployState(d) : { state: "never deployed" };
  const live = current(s);
  return { project: s.project, env: s.env, service: s.form.name, kind: s.form.kind, url: url(s), deploy_id: d?.id, state: st.state,
    commit: d?.commit, cause: "cause" in st ? st.cause : undefined, health: live && deployState(live).state === "live" ? "ok" : "unknown" };
}

function entries(p: string, e: string, service?: string) {
  const own = vars.filter((v) => v.project === p && v.env === e && (service === undefined || v.service === "" || v.service === service))
    .map((v) => ({ key: v.key, scope: v.service ? "service" : "shared", service: v.service || undefined, build: v.build, ref: v.ref, updated_at: v.updated }));
  const on = envOf(p, e)?.server ?? "";
  const reach = service === undefined ? [] : shared.filter((v) => v.scope === "team" || v.servers.includes(on)).map((v) => ({ key: v.key, scope: v.scope, build: v.build, updated_at: v.updated_at }));
  return [...own, ...reach];
}

function plain(v: Var): string {
  if (v.ref) {
    const [svc, k] = v.ref.split(".");
    const t = vars.find((x) => x.project === v.project && x.env === v.env && x.service === svc && x.key === k.toUpperCase());
    return t ? plain(t) : "";
  }
  const e = envOf(v.project, v.env)!;
  return open(server(e.server).keys, v.agentBox!);
}

function compare(p: string, left: string, right: string): DiffRow[] {
  const rows = new Map<string, DiffRow>();
  const val = (v: Var) => plain(v);
  for (const v of vars.filter((x) => x.project === p && x.env === left)) {
    const o = vars.find((x) => x.project === p && x.env === right && x.service === v.service && x.key === v.key);
    rows.set(v.service + "/" + v.key, { service: v.service, key: v.key, state: !o ? "only_left" : val(v) === val(o) ? "same" : "different" });
  }
  for (const v of vars.filter((x) => x.project === p && x.env === right))
    if (!rows.has(v.service + "/" + v.key)) rows.set(v.service + "/" + v.key, { service: v.service, key: v.key, state: "only_right" });
  return [...rows.values()];
}

const MOVE_STEPS = ["stop", "backup", "switch_server", "restore", "deploy", "clean"];
const MOVE_MS = 2200;

function moveView(m: Move) {
  const n = Math.floor((now() - m.started) / MOVE_MS);
  const done = n >= MOVE_STEPS.length;
  const e = envOf(m.project, m.env)!;
  if (n >= 2 && e.server !== m.to) {
    for (const v of vars.filter((x) => x.project === m.project && x.env === m.env && x.agentBox)) {
      v.agentBox = seal(server(m.to).keys.pub, open(server(m.from).keys, v.agentBox!));
    }
    e.server = m.to;
    for (const s of services.filter((x) => x.project === m.project && x.env === m.env)) newDeploy(s, current(s)?.commit, false, { created: now() - STEP_MS });
  }
  return { id: m.id, from: m.from, to: m.to, step: done ? "clean" : MOVE_STEPS[n], state: done ? "done" : "running", cause: "", cleaned: done,
    lines: movePlanLines(m.project, m.env, m.to) };
}

function movePlanLines(p: string, e: string, to: string) {
  const env = envOf(p, e)!;
  const svcs = services.filter((s) => s.project === p && s.env === e);
  const dbs = svcs.filter((s) => s.form.kind === "postgres"), apps = svcs.filter((s) => s.form.kind !== "postgres");
  const ip = dashed(server(to).ip);
  return [
    `move ${e} from ${env.server} to ${to}`,
    ...(dbs.length ? [`stop ${apps.map((a) => a.form.name).join(", ")} on ${env.server} (${e} is down from here until the switch)`] : []),
    ...dbs.map((d) => `back up ${d.form.name} on ${env.server}`),
    ...dbs.map((d) => `create ${d.form.name} on ${to} and restore it from that backup`),
    ...apps.map((a) => `build and start ${a.form.name} on ${to}`),
    ...apps.filter((a) => a.form.kind === "web").map((a) => `switch ${a.form.name}-${e}.${ip}.sslip.io`),
    ...apps.flatMap((a) => a.form.domains.map((d) => `point ${d} at ${server(to).ip} (an A record you change)`)),
    ...dbs.map((d) => `stop ${d.form.name} on ${env.server} (its volume is kept)`),
  ];
}

type Res = { status: number; body?: unknown; text?: string; type?: string };
const ok = (body?: unknown): Res => ({ status: body === undefined ? 204 : 200, body });
const err = (status: number, text: string): Res => ({ status, text });

let signedIn = true;

function handle(method: string, path: string, q: URLSearchParams, body: any): Res {
  const m = (re: RegExp) => path.match(re);
  let x: RegExpMatchArray | null;

  if (path === "/api/session") {
    if (method === "GET") return signedIn ? ok({ claimed: true, email: "owner@acme.uz" }) : err(401, "Sign in.");
    if (method === "POST") return (signedIn = true), ok({ email: body.email });
    if (method === "DELETE") return (signedIn = false), ok();
  }
  if (path === "/api/invites/accept") return (signedIn = true), ok({ joined: true });
  if (path === "/api/password-reset") return ok();
  if (!signedIn) return err(401, "Sign in.");
  if ((x = m(/^\/api\/cli-login\/([^/]+)\/(info|approve)$/))) return x[2] === "info"
    ? ok({ host: "behzods-macbook", from: "203.0.113.7", at: now() - 40_000, approved: false }) : ok({ check: "QF7-KM2" });

  if (path === "/api/templates" && method === "GET") return ok(TEMPLATES.map(({ id, title, line, category, new: fresh, memory, icons, services: svcs }) => ({ id, title, line, category, new: fresh, memory, icons,
    services: svcs.map((v) => ({ name: v.name, kind: v.kind, volumes: v.volumes ?? [] })) })));
  if ((x = m(/^\/api\/templates\/([^/]+)$/)) && method === "GET") {
    const t = TEMPLATES.find((v) => v.id === x![1]);
    return t ? ok({ ...t, services: t.services.map((v) => form(v.name, v.kind, v)) }) : err(404, "no such template");
  }
  if ((x = m(/^\/api\/environments\/([^/]+)\/([^/]+)\/templates\/([^/]+)$/)) && method === "POST") {
    const [, p, e, tid] = x;
    const t = TEMPLATES.find((v) => v.id === tid);
    if (!t) return err(404, "no such template");
    const taken = t.services.find((v) => services.some((x) => x.project === p && x.env === e && x.form.name === v.name));
    if (taken) return err(409, `${taken.name} already exists in ${p}/${e}.`);
    if (q.get("dry") !== "1") t.services.forEach((v) => newDeploy(addFresh(p, e, form(v.name, v.kind, v)), "", false, { subject: v.image ?? "", digest: v.image ? "sha256:" + "5e1d".repeat(16) : undefined }));
    return ok({ changes: t.services.map((v) => ({ op: "create", kind: "service", name: v.name })) });
  }

  if (path === "/api/projects" && method === "GET") {
    const names = [...new Set(envs.map((e) => e.project))];
    return ok(names.map((p) => ({ name: p, envs: envs.filter((e) => e.project === p).map((e) => ({ name: e.name, server: e.server, preview: e.preview,
      services: services.filter((s) => s.project === p && s.env === e.name).map(status) })) })));
  }
  if (path === "/api/projects" && method === "POST") {
    if (envs.some((e) => e.project === body.name)) return err(409, `A project named ${body.name} exists.`);
    envs.push({ project: body.name, name: "production", server: body.server });
    return ok({ name: body.name });
  }
  if (path === "/api/environments" && method === "POST") {
    envs.push({ project: body.project, name: body.env, server: body.server });
    return ok();
  }
  if (path === "/api/trash") return ok(trashed.map(({ envs: _e, services: _s, ...t }) => t));
  if ((x = m(/^\/api\/trash\/([^/]+)\/([^/]+)\/restore$/))) {
    const i = trashed.findIndex((t) => t.id === x![2]);
    if (i < 0) return err(404, "This is not in the trash.");
    const [t] = trashed.splice(i, 1);
    envs.push(...t.envs);
    services.push(...t.services);
    audit(`restored a ${t.kind} from the trash`);
    return { status: 202 };
  }
  if (path === "/api/audit" || path === "/api/audit.csv") {
    const who = q.get("actor") ?? "", text = (q.get("q") ?? "").toLowerCase();
    const rows = auditLog.filter((r) => (!who || r.actor === who) && (q.get("tokens") !== "1" || r.actor.startsWith("API token ")) && (!text || r.text.toLowerCase().includes(text) || text === `"service":"${r.service}"`));
    rows.sort((a, b) => b.at - a.at);
    const cell = (v: string) => `"${v.replaceAll('"', '""')}"`;
    if (path.endsWith(".csv")) return { status: 200, type: "text/csv", text: ["at,actor,text", ...rows.map((r) => [new Date(r.at).toISOString(), r.actor, r.text].map(cell).join(","))].join("\n") + "\n" };
    return ok({ rows, more: false });
  }
  if ((x = m(/^\/api\/environments\/([^/]+)\/([^/]+)$/)) && method === "DELETE") {
    if (body?.confirm !== x[2]) return err(400, `Type ${x[2]} to delete it.`);
    toTrash("environment", `env_${x[1]}_${x[2]}`, `${x[1]}/${x[2]}`, (e) => e.project === x![1] && e.name === x![2], (s) => s.project === x![1] && s.env === x![2]);
    return ok();
  }
  if ((x = m(/^\/api\/projects\/([^/]+)$/)) && method === "DELETE") {
    if (body?.confirm !== x[1]) return err(400, `Type ${x[1]} to delete it.`);
    toTrash("project", `prj_${x[1]}`, x[1], (e) => e.project === x![1], (s) => s.project === x![1]);
    return ok();
  }
  if ((x = m(/^\/api\/servers\/([^/]+)\/metrics$/))) return ok({ ...series(q.get("range") ?? "24h", x[1], true), services: servicesOn(x[1]) });
  if (path === "/api/servers") return ok(servers.map((s, i) => ({ id: s.id, name: s.name, public_ip: s.ip, agent_version: s.version,
    os_name: s.os, os_id: s.osId, kernel: s.kernel, cores: s.cores, booted_at: s.booted, mem_total: s.mem, disk_total: s.diskTotal,
    last_heartbeat: now() - 4000, disk_used_pct: s.disk, offline: false, reboot_required: s.name === "hel1", forecast: { days: Math.floor((100 - s.disk) * 0.8 / 1.2), gb_per_day: 1.2, growing: true },
    private_ip: `10.73.0.${i + 1}`, peers: servers.filter((p) => p.id !== s.id).map((p) => ({ name: p.name, last_handshake: now() - 20_000 - i * 7_000 })) })));
  if (path === "/api/join-token") return ok({ command: `yawl agent join https://panel.${dashed(servers[0].ip)}.sslip.io jt_${Math.random().toString(36).slice(2, 18)}` });
  if (path === "/api/inspect" && String((body as { repo?: string }).repo).endsWith("/storefront")) return ok({ commit: commits[3], dockerfile: false, language: "node 22", reads_port: true, port: 3000,
    health_path: "/", missing: ["NEXT_PUBLIC_API_URL"], warnings: [], builder: "", static: false, notes: [], framework: "nextjs" });
  if (path === "/api/inspect") return ok({ commit: commits[3], dockerfile: true, language: "go", reads_port: true, port: 10000,
    health_path: "/health", missing: ["RESEND_API_KEY"], warnings: [], builder: "", static: false, notes: [] });

  if ((x = m(/^\/api\/environments\/([^/]+)\/([^/]+)\/services$/)) && method === "POST") {
    const [, p, e] = x;
    const f = body as ServiceForm, ex = svcOf(p, e, f.name);
    if (q.get("new") && ex) return err(409, `${p}/${e} already has a service named ${f.name}.`);
    if (q.get("dry")) return ok({ changes: ex ? [{ op: "update", kind: f.kind, name: f.name, detail: diff(ex.form, f) }] : [{ op: "create", kind: f.kind, name: f.name }] });
    if (ex) { ex.form = f; ex.settingsAt = now(); } else addFresh(p, e, f);
    return ok();
  }
  if ((x = m(/^\/api\/environments\/([^/]+)\/([^/]+)\/keys$/))) {
    const e = envOf(x[1], x[2]);
    if (!e) return err(404, "No such environment.");
    return ok({ agent_pubkey: b64(server(e.server).keys.pub), recovery_pubkey: b64(recovery.pub), server: e.server });
  }
  if ((x = m(/^\/api\/environments\/([^/]+)\/([^/]+)\/env$/))) return ok(entries(x[1], x[2]));
  if ((x = m(/^\/api\/environments\/([^/]+)\/([^/]+)\/env\/([^/]+)$/))) return putVar(method, x[1], x[2], "", decodeURIComponent(x[3]), body);
  if ((x = m(/^\/api\/environments\/([^/]+)\/([^/]+)\/moves\/latest$/))) {
    const mv = [...moves].reverse().find((v) => v.project === x![1] && v.env === x![2]);
    return mv ? ok(moveView(mv)) : err(404, "no move");
  }
  if ((x = m(/^\/api\/environments\/([^/]+)\/([^/]+)\/move$/))) {
    const [, p, e] = x, env = envOf(p, e)!, to = method === "GET" ? q.get("to")! : body.to;
    if (to === env.server) return err(409, `${e} already runs on ${to}.`);
    if (method === "GET") {
      const sealed = vars.filter((v) => v.project === p && v.env === e && v.recoveryBox)
        .map((v) => ({ id: `${v.service}/${v.key}`, key: v.key, service: v.service, recovery_box: v.recoveryBox!, updated_at: v.updated }));
      return ok({ lines: movePlanLines(p, e, to), to_pubkey: b64(server(to).keys.pub), recovery_pubkey: b64(recovery.pub),
        restore: services.some((s) => s.project === p && s.env === e && s.form.kind === "postgres"), vars: sealed });
    }
    for (const [vid, r] of Object.entries(body.boxes as Record<string, { agent_box: string }>)) {
      try { open(server(to).keys, r.agent_box); } catch { return err(400, `${vid} is not sealed to ${to}.`); }
    }
    moves.push({ id: id("mv_"), project: p, env: e, from: env.server, to, started: now(), cause: "", failed: false });
    return ok();
  }

  if ((x = m(/^\/api\/services\/([^/]+)\/([^/]+)\/([^/]+)(\/.*)?$/))) {
    const [, p, e, name, rest = ""] = x;
    const s = svcOf(p, e, name);
    if (!s) return err(404, `${p}/${e} has no service named ${name}.`);
    const env = envOf(p, e)!;
    if (rest === "" && method === "GET") {
      const d = current(s);
      return ok({ id: s.id, stopped: !!s.stopped, maintenance: !!s.maintenance, password_user: s.passwordUser, public: !!s.public, form: s.form, memory_limit: memBytes(s.form.memory), url: url(s), health: d && deployState(d).state === "live" ? "ok" : "unknown", server: env.server, current_deploy_id: d?.id,
        domains: s.form.domains.map((h) => (h.startsWith("api.") ? { host: h, dns_state: "wrong", cert_state: "pending" } : { host: h, dns_state: "ok", cert_state: "issued" })),
        settings_at: s.settingsAt, skip_words: s.form.repo && !s.form.image ? ["[skip ci]", "[ci skip]", "[skip deploy]", "[skip yawl]"] : undefined, preview_max: 5, preview_idle_days: 7,
        used_by: vars.filter((v) => v.project === s.project && (v.ref === `${s.form.name}.url` && v.env === s.env || v.ref === `${s.env}/${s.form.name}.url`)).map((v) => ({ env: v.env, service: v.service })) });
    }
    if (rest === "/deploys" && method === "GET") return ok([...s.deploys].reverse().map((d) => {
      const st = deployState(d);
      return { id: d.id, state: st.state, commit: d.commit, cause: st.cause, rollback: d.rollback, created_at: d.created,
        finished_at: ["live", "failed"].includes(st.state) ? d.created + steps(d, s).length * STEP_MS : undefined, ...words(d) };
    }));
    if (rest === "/deploys" && method === "POST") return ok({ deploy_id: newDeploy(s).id });
    if (rest === "/import") return ok({ deploy: newDeploy(s, "", false, { subject: `Import ${q.get("name") ?? body?.url ?? body?.path ?? "a dump"}` }).id });
    if (rest === "/rollback") {
      const prev = body?.to ? s.deploys.find((d) => d.id === body.to && words(d).can_roll_back)
        : rollbackTarget(s);
      if (!prev) return err(409, "There is no earlier version to go back to.");
      return ok({ deploy_id: newDeploy(s, prev.commit, true).id });
    }
    if (rest === "" && method === "DELETE") {
      if (body?.confirm !== name) return err(400, `Type ${name} to delete it.`);
      toTrash("service", s.id, `${p}/${e}/${name}`, () => false, (v) => v.id === s.id);
      return ok();
    }
    if (rest === "/delete-info") {
      const users = s.form.kind === "postgres" ? services.filter((v) => v.project === p && v.env === e && v.form.kind !== "postgres").map((v) => v.form.name) : [];
      return ok({ purge_at: now() + 48 * 3_600_000, users });
    }
    if (rest === "/runs") {
      if (s.form.kind !== "cron") return err(404, `${name} is not a cron job.`);
      if (method === "POST") {
        cronRuns(s).unshift({ ID: id("run_"), ScheduledAt: now(), StartedAt: now(), FinishedAt: now() + 3200, Exit: 0, Cause: "", Output: "report sent to 14 people", Skipped: false, TimedOut: false });
        return { status: 202 };
      }
      const tz = s.form.timezone || "UTC";
      const nextMin = Math.ceil((now() + 1) / 3_600_000) * 3_600_000;
      return ok({ next: nextMin, timezone: tz, running: false, runs: cronRuns(s) });
    }
    if (rest === "/data/tables") return ok({ tables: Object.keys(DATA).map((t) => ({ schema: "public", name: t })) });
    if (rest === "/data/rows") return DATA[body.table] ? ok({ ...DATA[body.table], rows: body.page ? [] : DATA[body.table].rows, more: false }) : err(400, `relation "${body.table}" does not exist`);
    if (rest === "/data/write") return ok({ changed: 1 });
    if (rest === "/data/query") {
      const t = Object.keys(DATA).find((k) => String(body.sql).toLowerCase().includes(k));
      return t ? ok(DATA[t]) : err(400, `The demo answers queries on ${Object.keys(DATA).join(", ")} only.`);
    }
    if (rest === "/public" && method === "PUT") return (s.public = !!body.public), ok();
    if (rest === "/keys") return ok({ agent_pubkey: b64(server(env.server).keys.pub), recovery_pubkey: b64(recovery.pub), server: env.server });
    if (rest === "/env") return ok(entries(p, e, name));
    if ((x = rest.match(/^\/env\/([^/]+)\/reveal$/))) {
      const key = decodeURIComponent(x[1]);
      const v = vars.find((y) => y.project === p && y.env === e && y.key === key && (y.service === name || y.service === ""));
      if (!v) return err(404, `${name} has no ${key}.`);
      return ok({ box: seal(sodium.from_base64(body.to, V()), plain(v)) });
    }
    if ((x = rest.match(/^\/env\/([^/]+)$/))) return putVar(method, p, e, name, decodeURIComponent(x[1]), body);
    if (rest === "/logs") return ok({ Lines: logs(s, q) });
    if (rest === "/requests") return s.form.kind === "web" ? ok(requests(q)) : err(404, `${name} takes no HTTP requests.`);
    if ((x = rest.match(/^\/crawlers\/([^/]+)$/)) && method === "PUT") {
      const c = decodeURIComponent(x[1]);
      if (body?.blocked) blockedCrawlers.add(c); else blockedCrawlers.delete(c);
      return ok();
    }
    if (rest === "/metrics") return ok(series(q.get("range") ?? "1h", s.id, false));
    if (rest === "/password" && method === "PUT") return (s.passwordUser = body?.user || undefined), ok();
    if (rest === "/maintenance" && method === "PUT") return (s.maintenance = !!body?.on), ok();
    if (rest === "/stop" && method === "POST") return (s.stopped = true), ok({ note: "" });
    if ((rest === "/start" || rest === "/restart") && method === "POST") {
      s.stopped = false;
      return ok({ deploy_id: newDeploy(s, current(s)?.commit ?? commits[3], true, { rollback: true }).id });
    }
    if (rest === "/pitr") return ok(s.form.pitr
      ? { pitr: true, from: now() - 7 * day + 3 * hour, to: now() - 3 * 60_000, shipped: now() - 40_000 } : { pitr: false, from: 0, to: 0, shipped: 0 });
    if (rest === "/backups" && method === "GET") return ok(backups.filter((b) => b.svc === s.id).reverse().map((b) => {
      const ready = now() - b.created > 4000;
      return { id: b.id, state: ready ? "ok" : "running", size: ready ? b.size : undefined, created_at: b.created, finished_at: ready ? b.created + 4000 : undefined, volume: b.volume };
    }));
    if (rest === "/backups" && method === "POST") {
      if (!storage.set) return err(409, "Set backup storage first, in Settings.");
      backups.push({ id: id("bak_"), svc: s.id, created: now(), size: body?.volume ? 219_000_000 : 39_600_000, volume: body?.volume });
      return ok({ id: backups[backups.length - 1].id });
    }
    if (rest === "/restore" && s.form.kind !== "postgres") {
      return ok({ deploy: newDeploy(s, current(s)?.commit ?? commits[3], true, { restore: true }).id });
    }
    if (rest === "/restore") {
      const into = svcOf(p, e, body.into) ?? addFresh(p, e, form(body.into, "postgres"), true);
      return ok({ deploy: newDeploy(into, "", false, { restore: true }).id });
    }
  }
  if (path === "/api/members" && method === "GET") return ok(members);
  if (/^\/api\/servers\/[^/]+\/firewall$/.test(path) && method === "GET") return ok({
    rules: { on: true, allow: [{ proto: "tcp", port: 6543, from: ["203.0.113.0/24"] }], block: ["198.51.100.7"], ssh_from: [] },
    yawl: [{ proto: "tcp", port: 22, why: "SSH (the port sshd listens on)" }, { proto: "tcp", port: 80, why: "Web traffic" }, { proto: "both", port: 443, why: "Web traffic" }],
    your_address: "203.0.113.7", telegram: false,
    status: { on: true, available: true, ufw: false, version: "a1b2", bans: [{ ip: "192.0.2.21", until: Date.now() + 40 * 60_000 }],
      ports: [{ proto: "tcp", port: 22, address: "0.0.0.0", process: "sshd", exposure: "everyone" }, { proto: "tcp", port: 80, address: "0.0.0.0", process: "caddy", exposure: "everyone" },
        { proto: "tcp", port: 443, address: "0.0.0.0", process: "caddy", exposure: "everyone" }, { proto: "tcp", port: 6379, address: "0.0.0.0", process: "redis-server", exposure: "closed" }] },
  });
  if (method === "POST" && /^\/api\/(settings\/(storage|gitea\/[^/]+|gitlab\/[^/]+)|registries\/[^/]+)\/test$/.test(path)) return ok();
  if (path === "/api/settings/gitlab" && method === "GET") return ok(gitlabHosts.map((host) => ({ host, webhook_url: location.origin + "/gitlab/webhook" })));
  if ((x = path.match(/^\/api\/settings\/gitlab\/([^/]+)$/))) {
    const host = decodeURIComponent(x[1]);
    if (method === "DELETE") return (gitlabHosts = gitlabHosts.filter((h) => h !== host)), ok(null);
    if (!gitlabHosts.includes(host)) gitlabHosts.push(host);
    return ok({ host, webhook_url: location.origin + "/gitlab/webhook", secret: "3f9c1a7e5b2d4c6f8a0e1b3d5f7a9c2e" });
  }
  if (path === "/api/settings/notifications" && method === "GET") return ok({ slack: { set: true, host: "hooks.slack.com" }, discord: { set: false, host: "" }, webhook: { set: false, host: "" }, ntfy: { set: false, host: "" },
    email: { set: false, host: "", port: "", username: "", from: "", to: "" } });
  if (path === "/api/me" && method === "GET") return ok({ email: "owner@acme.uz", role: "admin" });
  if (path === "/api/account/two-step" && method === "GET") return ok({ on: true, codes_left: 9, required: false });
  if (path === "/api/invites" && method === "GET") return ok(invites);
  if (path === "/api/invites" && method === "POST") {
    invites.push({ id: id("inv_"), role: body.role, expires_at: now() + 7 * day });
    return ok({ token: Math.random().toString(36).slice(2, 18), id: invites[invites.length - 1].id });
  }
  if ((x = m(/^\/api\/invites\/([^/]+)$/)) && method === "DELETE") return (invites.splice(invites.findIndex((i) => i.id === x![1]), 1), ok(null));
  if ((x = m(/^\/api\/members\/([^/]+)$/))) {
    const i = members.findIndex((y) => y.id === x![1]);
    if (method === "DELETE") members.splice(i, 1);
    else if (i >= 0) members[i].role = body.role;
    return ok(null);
  }
  if ((x = m(/^\/api\/projects\/([^/]+)\/previews$/))) return ok(x[1] !== "acme" ? [] : previews.filter((p) => p.state !== "closed"));
  if ((x = m(/^\/api\/previews\/([^/]+)\/close$/))) {
    const p = previews.find((y) => y.id === x![1]);
    if (p) p.state = "closed";
    return ok(null);
  }
  if ((x = m(/^\/api\/projects\/([^/]+)\/compare$/))) return ok(compare(x[1], body.left, body.right));
  if ((x = m(/^\/api\/deploys\/([^/]+)$/))) {
    const d = services.flatMap((s) => s.deploys).find((v) => v.id === x![1]);
    return d ? ok(deployView(d)) : err(404, "No such deploy.");
  }

  if (path === "/api/recovery-key") return method === "GET" ? ok({ set: true, fingerprint: b64(recovery.pub).slice(0, 8) }) : ok();
  if (path === "/api/adopt") return method === "GET" ? ok({ adopting: false, lines: [], seen: "" }) : ok();
  if (path === "/api/settings/storage") {
    if (method === "PUT") storage = { set: true, endpoint: body.endpoint, bucket: body.bucket, region: body.region, access_key_end: String(body.access_key).slice(-4) };
    return method === "PUT" ? ok() : ok(storage);
  }
  if (path === "/api/settings/telegram") {
    if (method === "POST") return (tg = { state: "pending", bot: "acme_alerts_bot", code: "yawl-482913" }), (tgPolls = 0), ok(tg);
    if (method === "DELETE") return (tg = { state: "none" }), ok();
    if (tg.state === "pending" && ++tgPolls > 4) (tg = { state: "linked", bot: tg.bot }), (relay.chats = []);
    let ended: typeof relay.ended;
    if (relay.pending && ++relay.polls > 3) {
      const group = relay.pending.link.includes("?startgroup=");
      const want = demoChats.find((c) => (c.type !== "private") === group)!;
      if (relay.chats.some((x) => x.id === want.id)) ended = { why: "already", title: want.title };
      else relay.chats.push({ ...want, linked_at: now() });
      relay.pending = undefined;
      tg = { state: "none" };
    }
    const chats = relay.chats.map((c) => ({ ...c, routes: relay.routes[c.id] ?? { servers: [], projects: [] } }));
    return ok({ ...tg, relay: { available: true, bot: "useyawl_bot", chats, pending: relay.pending, ended } });
  }
  if (path === "/api/settings/telegram/test") return ok();
  if (path === "/api/settings/telegram/connect") {
    if (method === "DELETE") return (relay.pending = undefined), ok();
    const code = "Yq7cTz2mB4kXv9Lp";
    const link = `https://t.me/useyawl_bot?${body?.group ? "startgroup" : "start"}=` + code;
    relay.pending = { code, link, expires_at: now() + 600_000, qr: demoQR(link), bot: "useyawl_bot" };
    relay.polls = 0;
    return ok(relay.pending);
  }
  if ((x = m(/^\/api\/settings\/telegram\/chats\/([^/]+)(\/test|\/routes)?$/))) {
    const chat = decodeURIComponent(x[1]);
    if (x[2] === "/routes") return (relay.routes[chat] = { servers: body.servers ?? [], projects: body.projects ?? [] }), ok();
    if (x[2]) return ok();
    relay.chats = relay.chats.filter((c) => c.id !== chat);
    delete relay.routes[chat];
    return ok();
  }
  if (path === "/api/settings/watch") {
    if (method === "PUT") {
      if (tg.state !== "linked" && !relay.chats.length) return err(400, "Link Telegram first: the watcher sends its alerts there.");
      watchOn = true;
      if (!incidents.length) {
        const t = now() - 2 * 86_400_000;
        incidents.push({ id: "inc_demo1", subject: "server:srv_1", kind: "server", label: servers[0].name, source: "watch", opened_at: t, closed_at: t + 7 * 60_000,
          cause: `${servers[0].name} is not answering. 2 services on it are not checked separately until it is back.`, note: "" });
      }
    }
    if (method === "DELETE") {
      watchOn = false;
      return ok({ state: "off", note: "The watcher stays in your Cloudflare account; delete yawl-watch-3f9a2c1d there to remove it.", checks: 0, servers: [] });
    }
    if (!watchOn) return ok({ state: "off", checks: 0, servers: [] });
    return ok({ state: "on", url: WATCH_URL, account: "acc_demo", checks: services.length + servers.length + 1, last_run: now() - 12_000,
      servers: servers.map((v) => ({ name: v.name, state: "watched" })) });
  }
  if (path === "/api/checks") {
    if (!watchOn) return ok({ off: true });
    return ok({ at: now() - 20_000, results: Object.fromEntries(services.map((v) => ["service:" + v.id, { ok: true, ms: 80 + (v.id.charCodeAt(v.id.length - 1) % 90), detail: "", since: now() - 86_400_000 }])) });
  }
  if (path === "/api/incidents") return watchOn ? ok(incidents) : ok({ off: true });
  if ((x = m(/^\/api\/incidents\/([^/]+)\/note$/))) {
    const i = incidents.find((v) => v.id === x![1]);
    if (i) i.note = body.text;
    return ok();
  }
  if (path === "/api/heartbeats") {
    if (method === "POST") {
      if (!watchOn) return err(400, "Deploy the watcher first: heartbeats are checked from Cloudflare.");
      if (Number(body.period_s) < 60) return err(400, "A heartbeat's period is at least 60 seconds.");
      const h = { id: id("hb_"), name: body.name, period_s: body.period_s, grace_s: body.grace_s, url: WATCH_URL + "/hb/" + Math.random().toString(16).slice(2).padEnd(32, "0").slice(0, 32) };
      heartbeats.push(h);
      return ok(h);
    }
    return ok(heartbeats);
  }
  if ((x = m(/^\/api\/heartbeats\/([^/]+)$/))) return heartbeats.splice(heartbeats.findIndex((h) => h.id === x![1]), 1), ok();
  if (path === "/api/settings/panel") {
    if (method === "PUT") return (panelDomain = body.domain), ok();
    return ok({ domain: panelDomain, hosts: panelDomain ? [{ host: panelDomain, dns_state: "wrong", dns_detail: `${panelDomain} has no A record yet. Set ${panelDomain} → ${servers[0].ip}.`, cert_state: "pending" }] : [] });
  }
  if (path === "/api/tokens") {
    if (method === "POST") {
      const t = { id: id("tok_"), name: body.name, last_used_at: 0, created_at: now() };
      tokens.push(t);
      return ok({ token: "yawl_" + Math.random().toString(36).slice(2) + Math.random().toString(36).slice(2) });
    }
    return ok(tokens);
  }
  if ((x = m(/^\/api\/tokens\/([^/]+)$/))) return tokens.splice(tokens.findIndex((t) => t.id === x![1]), 1), ok();
  if (path === "/api/update") return method === "POST" ? err(409, "The demo runs the newest release, 1.0.0.") : ok({ current: "1.0.0", latest: "1.0.0" });
  if (path === "/api/account/password") return body.old === "correct horse battery" ? ok() : err(403, "That is not your current password. (In the demo it is: correct horse battery)");
  if (path === "/api/github") return ok({ connected: false });
  if (path === "/api/github/repos") return ok([["acme/storefront", 0.2, false, "main"], ["acme/api", 0.4, true, "main"], ["acme/web", 3, false, "main"], ["acme/worker", 26, true, "trunk"], ["acme/landing", 75, false, "main"], ["acme/docs", 400, false, "gh-pages"], ["acme/old-crm", 2900, true, "master"]]
    .map(([name, hours, priv, branch]) => ({ full_name: name, url: `https://github.com/${name}`, private: priv, pushed_at: now() - (hours as number) * 3_600_000, default_branch: branch })));
  if (path === "/api/github/connect") return err(409, "GitHub is not connected in this demo. On your panel this opens GitHub to make the app.");
  if (path === "/api/settings/gitea" && method === "GET") return ok(giteaHosts.map((host) => ({ host, webhook_url: location.origin + "/gitea/webhook" })));
  if ((x = m(/^\/api\/settings\/gitea\/([^/]+)$/))) {
    const host = decodeURIComponent(x[1]);
    if (method === "DELETE") return (giteaHosts = giteaHosts.filter((h) => h !== host)), ok(null);
    if (!giteaHosts.includes(host)) giteaHosts.push(host);
    return ok({ host, webhook_url: location.origin + "/gitea/webhook", secret: "8d1e0c2b4a6f9e7d5c3b1a0f2e4d6c8b" });
  }
  if (path === "/api/registries") return ok(registries);
  if ((x = m(/^\/api\/registries\/([^/]+)$/))) {
    const host = decodeURIComponent(x[1]);
    const i = registries.findIndex((r) => r.host === host);
    if (method === "DELETE") return i >= 0 && registries.splice(i, 1), ok(null);
    if (i >= 0) registries[i].username = body.username; else registries.push({ host, username: body.username });
    return ok(null);
  }
  if (path === "/api/settings/builds") {
    if (method === "GET") return ok(builds);
    if (body.server && !registries.some((r) => r.host === String(body.repository).split("/")[0])) return err(400, `Add a login for ${String(body.repository).split("/")[0]} in Integrations, Registries first: the build server pushes there.`);
    return (builds = body.server ? { server: body.server, repository: body.repository } : { server: "", repository: "" }), ok(null);
  }
  if (path === "/api/settings/sso") {
    if (method === "GET") return ok({ ...sso, works: false });
    return (sso = { name: body.name, issuer: body.issuer, client_id: body.client_id, only: !!body.only && !!body.issuer, secret_set: sso.secret_set || !!body.client_secret }), ok(null);
  }
  if (path === "/api/shared-env" && method === "GET") return ok(shared);
  if (path === "/api/shared-env/keys") return ok({ servers: servers.map((v) => ({ name: v.name, agent_pubkey: b64(v.keys.pub) })), recovery_pubkey: b64(recovery.pub) });
  if ((x = m(/^\/api\/shared-env\/(team|servers\/([^/]+))\/([^/]+)$/))) {
    const key = decodeURIComponent(x[3]);
    const scope = x[1] === "team" ? "team" : "server";
    const on = scope === "team" ? servers.map((v) => v.name) : [decodeURIComponent(x[2])];
    const i = shared.findIndex((v) => v.scope === scope && v.key === key && (scope === "team" || v.servers[0] === on[0]));
    if (method === "DELETE") return i >= 0 && shared.splice(i, 1), ok(null);
    const row = { scope: scope as "team" | "server", key, servers: on, build: !!body.build, updated_at: now() };
    if (i >= 0) shared[i] = row; else shared.push(row);
    return ok(null);
  }
  if ((x = m(/^\/api\/environments\/([^/]+)\/([^/]+)\/sources(?:\/([a-z]+))?$/))) {
    const k = x[1] + "/" + x[2];
    const list = (sources[k] ??= []);
    if (!x[3]) return ok(list);
    const i = list.findIndex((v) => v.provider === x![3]);
    if (method === "DELETE") return i >= 0 && list.splice(i, 1), ok(null);
    const row = { provider: x[3], url: body.url, path: body.path, workspace: body.workspace, env: body.env, updated_at: now() };
    if (i >= 0) list[i] = row; else list.push(row);
    return ok(null);
  }
  if ((x = m(/^\/api\/servers\/([^/]+)\/(cleanup|volumes\/unused|volumes\/[^/]+|os|os\/upgrade|drain|tailscale)$/))) {
    const sv = server(decodeURIComponent(x[1]));
    const what = x[2];
    if (what === "cleanup") return ok({ at: now() - (method === "POST" ? 0 : 7 * hour), why: method === "POST" ? "now" : "nightly", containers: 3, images: 5, freed: 1_420_000_000 });
    if (what === "volumes/unused") return ok(["yawl_blog_production_old_cache"]);
    if (what.startsWith("volumes/")) return ok(null);
    if (what === "os") return ok({ Updates: 12, Security: 5, Reboot: sv.name === "hel1", RebootPkgs: sv.name === "hel1" ? ["linux-image-6.8.0-48-generic"] : [], Auto: true, Installer: true, Running: false,
      Log: "2026-11-01 06:12:01,123 INFO Starting unattended upgrades script\n2026-11-01 06:12:40,001 INFO All upgrades installed" });
    if (what === "os/upgrade") return ok(null);
    if (what === "drain") {
      if (method === "PUT") return (drains[sv.name] = { url: body.url, user: body.user }), ok(null);
      if (method === "DELETE") return delete drains[sv.name], ok(null);
      const d = drains[sv.name];
      return ok({ set: !!d, url: d?.url ?? "", user: d?.user ?? "", agent_pubkey: b64(sv.keys.pub) });
    }
    if (method === "PUT") return (tailnet[sv.name] = "100.101.102." + (10 + servers.indexOf(sv))), ok(null);
    if (method === "DELETE") return delete tailnet[sv.name], ok(null);
    return ok({ running: !!tailnet[sv.name], address: tailnet[sv.name], agent_pubkey: b64(sv.keys.pub) });
  }
  if ((x = m(/^\/api\/services\/([^/]+)\/([^/]+)\/([^/]+)\/analytics$/))) {
    const range = q.get("range") || "24h";
    const n = range === "24h" ? 24 : range === "7d" ? 7 * 24 : 30;
    const step = range === "30d" ? day : hour;
    const series = Array.from({ length: n }, (_, i) => {
      const views = Math.round(40 + 30 * Math.sin(i / 3) + (i % 5) * 4);
      return { at: now() - (n - i) * step, views, visitors: Math.round(views * 0.6), requests: views * 7, errors: i % 9 === 0 ? 2 : 0, bytes: views * 180_000 };
    });
    const who = Array.from({ length: range === "24h" ? 24 : range === "7d" ? 7 : 30 }, (_, i) => i).map((i, _, all) => {
        const humans = Math.round((range === "7d" ? 5900 : 250) * (1 + 0.25 * Math.sin(i / 2)));
        return { at: now() - (all.length - 1 - i) * (range === "24h" ? hour : day), humans, bots: Math.round(humans * 0.28), ai: Math.round(humans * (0.1 + 0.08 * (i / all.length))) };
      });
    const aiTotal = who.reduce((a, b) => a + b.ai, 0);
    const crawlerTotal = CRAWLERS.reduce((a, c) => a + c.n, 0);
    const sum = (k: "views" | "requests" | "errors" | "bytes") => series.reduce((a, b) => a + b[k], 0);
    return ok({ range, visitors: Math.round(sum("views") * 0.45), views: sum("views"), requests: sum("requests"), errors: sum("errors"), bytes: sum("bytes"),
      s2: sum("requests") - sum("errors") - 120, s3: 80, s4: 40, s5: sum("errors"), series,
      paths: [{ value: "/", n: 812 }, { value: "/pricing", n: 233 }, { value: "/docs", n: 140 }], refs: [{ value: "google.com", n: 301 }, { value: "t.me", n: 96 }],
      who,
      crawlers: CRAWLERS.map((c) => ({ ...c, n: Math.round((c.n * aiTotal) / crawlerTotal), blocked: blockedCrawlers.has(c.name) })),
      bots: [["Googlebot", 0.51], ["Bingbot", 0.21], ["Uptime monitors", 0.17], ["curl, scripts, tools", 0.075], ["Unknown bots", 0.035]].map(([value, f]) => ({ value, n: Math.round(who.reduce((a, b) => a + b.bots, 0) * (f as number)) })) });
  }
  if (m(/^\/api\/services\/[^/]+\/[^/]+\/[^/]+\/log-alerts$/)) return method === "PUT" ? ((logAlerts = body), ok(body)) : ok(logAlerts);
  if (m(/^\/api\/services\/[^/]+\/[^/]+\/[^/]+\/terminal$/)) return err(409, "The demo has no containers to open a shell in. On your panel this is a shell inside the service's container.");
  if (path === "/api/hetzner") return ok(hetzner
    ? { connected: true, made: hetznerMade, locations: [{ name: "hel1", city: "Helsinki" }, { name: "fsn1", city: "Falkenstein" }, { name: "ash", city: "Ashburn, VA" }],
        types: [{ name: "cx22", cores: 2, memory: 4, disk: 40, prices: { hel1: "4.51", fsn1: "4.51" } }, { name: "cx32", cores: 4, memory: 8, disk: 80, prices: { hel1: "8.09", fsn1: "8.09" } },
          { name: "cpx21", cores: 3, memory: 4, disk: 80, prices: { hel1: "9.20", fsn1: "9.20", ash: "9.20" } }] }
    : { connected: false });
  if (path === "/api/settings/hetzner") return (hetzner = method === "PUT"), ok(null);
  if (path === "/api/hetzner/servers") {
    if (!/^[a-z][a-z0-9-]{0,30}$/.test(body.name)) return err(400, "A server's name is lowercase letters, digits and dashes, starting with a letter, like fsn2.");
    const ip = "203.0.113." + (20 + hetznerMade.length);
    hetznerMade.push({ name: body.name, ip, at: now(), late: false });
    return ok({ id: 4200 + hetznerMade.length, ip });
  }
  if ((x = m(/^\/api\/environments\/([^/]+)\/([^/]+)\/export$/))) {
    const list = services.filter((v) => v.project === x![1] && v.env === x![2]);
    const e = envOf(x[1], x[2]);
    if (!e) return err(404, "No such environment.");
    const lines = [`project: ${x[1]}`, `environment: ${x[2]}`, `server: ${e.server}`, "services:",
      ...list.flatMap((v) => [`  - name: ${v.form.name}`, `    kind: ${v.form.kind}`, ...(v.form.repo ? [`    repo: ${v.form.repo}`, `    branch: ${v.form.branch}`] : []),
        ...(v.form.image ? [`    image: ${v.form.image}`] : []), ...(v.form.port ? [`    port: ${v.form.port}`] : [])])];
    return { status: 200, text: lines.join("\n") + "\n" };
  }
  if ((x = m(/^\/api\/services\/([^/]+)\/([^/]+)\/([^/]+)\/copy$/))) {
    const from = svcOf(x[1], x[2], x[3]);
    if (!from) return err(404, `${x[1]}/${x[2]} has no service named ${x[3]}.`);
    if (!envOf(body.project, body.env)) return err(400, `There is no ${body.project}/${body.env}.`);
    if (svcOf(body.project, body.env, x[3])) return err(400, `${body.project}/${body.env} has a service ${x[3]} already.`);
    const db = !["web", "worker", "cron"].includes(from.form.kind);
    if (q.get("dry") === "1") return ok({ lines: [`copy ${x[3]} to ${body.project}/${body.env}, not deployed until you deploy it`,
      ...(db ? [`${x[3]} starts empty, with its own password; restore a backup into it for its data`] : [])], vars: [] });
    addService(body.project, body.env, { ...from.form, domains: [] }, 0);
    if (!db) vars.push(...vars.filter((v) => v.project === x![1] && v.env === x![2] && v.service === x![3] && !v.ref?.includes("/")).map((v) => ({ ...v, project: body.project, env: body.env })));
    if (body.move) services.splice(services.indexOf(from), 1);
    return ok({ path: `/p/${body.project}/${body.env}/s/${x[3]}` });
  }
  if (path === "/api/status-pages" && method === "GET") return ok({
    pages: statusPages.map((p) => ({ ...p, url: `https://${p.slug}.status.${dashed(servers.find((v) => v.id === p.server_id)!.ip)}.sslip.io`,
      hosts: p.domain ? [{ host: p.domain, dns: "ok", dns_detail: "", cert: "issued" }] : [] })),
    choices: [{ subject: "panel", label: "The panel" }, ...[...new Set(envs.map((e) => e.project))].map((p) => ({ subject: "project:" + p, label: `Project ${p}, every service in it` })),
      ...services.map((v) => ({ subject: "service:" + v.id, label: `${v.project}/${v.env}/${v.form.name}` }))],
    servers: servers.map((v) => ({ id: v.id, name: v.name })),
  });
  if ((x = m(/^\/api\/status-pages(?:\/([^/]+))?$/))) {
    const i = statusPages.findIndex((p) => p.id === x![1]);
    if (x[1] && i < 0) return err(404, "no such status page");
    if (method === "DELETE") return i >= 0 && statusPages.splice(i, 1), ok();
    if (!String(body.title ?? "").trim()) return err(400, "A title is needed.");
    if (!servers.some((v) => v.id === body.server_id)) return err(400, "Choose a server to serve the page.");
    if (!/^[a-z0-9]([a-z0-9-]{0,30}[a-z0-9])?$/.test(body.slug)) return err(400, "The short name is lowercase letters, digits and dashes, up to 32.");
    if (statusPages.some((p) => p.id !== x![1] && p.slug === body.slug)) return err(400, `Another status page has the short name ${body.slug}.`);
    const page: StatusPage = { id: x[1] ?? id("sp_"), slug: body.slug, title: body.title.trim(), server_id: body.server_id, domain: String(body.domain ?? "").trim().toLowerCase(), watcher: !!body.watcher,
      rows: (body.rows ?? []).map((r: { id?: string; name: string; subject: string }) => ({ ...r, id: r.id?.startsWith("sr_") ? r.id : id("sr_") })) };
    if (i >= 0) statusPages[i] = page; else statusPages.push(page);
    return ok(page);
  }
  if ((x = m(/^\/api\/status-pages\/([^/]+)\/preview$/))) {
    const p = statusPages.find((v) => v.id === x![1]);
    if (!p) return err(404, "no such status page");
    const esc = (v: string) => v.replace(/[&<>"]/g, (c) => `&#${c.charCodeAt(0)};`);
    return { status: 200, type: "text/html", text: `<!doctype html><meta charset="utf-8"><title>${esc(p.title)}</title><body style="font:16px system-ui;max-width:640px;margin:40px auto;padding:0 16px">` +
      `<h1>${esc(p.title)}</h1><p>All systems operational.</p><ul>${p.rows.map((r) => `<li>${esc(r.name)}: operational</li>`).join("")}</ul></body>` };
  }
  if ((x = m(/^\/api\/status-pages\/([^/]+)\/incidents$/))) {
    if (method === "GET") return ok(statusIncidents.filter((v) => v.page_id === x![1]));
    if (body.kind === "maintenance" && (!body.starts_at || body.ends_at <= body.starts_at)) return err(400, "Maintenance needs a start and an end after it.");
    const inc: StatusIncident = { id: id("si_"), page_id: x[1], kind: body.kind, title: body.title, rows: body.rows ?? [], state: body.publish ? "published" : "draft", auto: false,
      starts_at: body.kind === "maintenance" ? body.starts_at : 0, ends_at: body.kind === "maintenance" ? body.ends_at : 0, resolved_at: 0, created_at: now(),
      updates: body.body?.trim() ? [{ id: id("su_"), at: now(), body: body.body.trim() }] : [] };
    statusIncidents.unshift(inc);
    return ok(inc);
  }
  if ((x = m(/^\/api\/status-incidents\/([^/]+)(\/updates|\/publish)?$/))) {
    const i = statusIncidents.findIndex((v) => v.id === x![1]);
    if (i < 0) return err(404, "no such incident");
    const inc = statusIncidents[i];
    if (method === "DELETE") return inc.auto && inc.state === "draft" ? (inc.state = "dismissed") : statusIncidents.splice(i, 1), ok();
    if (x[2] === "/publish") return (inc.state = "published"), ok();
    if (body.resolve && (inc.kind !== "incident" || inc.resolved_at)) return err(400, "Only an open incident can be resolved.");
    inc.updates.push({ id: id("su_"), at: now(), body: String(body.body).trim() });
    if (body.resolve) inc.resolved_at = now();
    return ok();
  }
  return err(404, `This demo does not answer ${method} ${path}.`);
}

function appCpu(at: number, salt: number): number {
  const rnd = (k: number) => { const x = Math.sin(at / 60_000 * 12.9898 + salt * 78.233 + k) * 43758.5453; return x - Math.floor(x); };
  const hod = (at % 86_400_000) / 3_600_000;
  const ago = (now() - at) / 3_600_000;
  if (ago > 97.85 && ago < 97.92) return 88 + rnd(1) * 6;
  if (ago > 26.4 && ago < 26.45) return 64 + rnd(2) * 8;
  return 6 + 7 * Math.max(0, Math.sin(((hod - 6) / 24) * 2 * Math.PI)) + rnd(3) * 4 + (rnd(4) < 0.01 ? rnd(5) * 10 : 0);
}
const busiest = (hour: number, salt: number) => Math.max(...Array.from({ length: 60 }, (_, m) => appCpu(hour + m * 60_000, salt)));

function series(range: string, seed: string, machine: boolean) {
  const hours = range === "7d" || range === "30d" || range === "90d";
  const step = hours ? 3_600_000 : 60_000;
  const n = { "1h": 60, "24h": 1440, "7d": 168, "30d": 720, "90d": 2160 }[range] ?? 60;
  const salt = [...seed].reduce((a, c) => a + c.charCodeAt(0), 0);
  const end = Math.floor(now() / step) * step;
  const points = [];
  for (let i = n; i > 0; i--) {
    const at = end - i * step;
    if (!hours && i > 40 && i < 46) continue;
    const day = Math.sin((at / 86_400_000) * 2 * Math.PI);
    const noise = Math.sin(at / 600_000 + salt) * 0.3;
    if (machine) {
      const sv = server(seed);
      const net = !hours && i === 40 ? null : 1.4e6 + 9e5 * day + 4e5 * noise;
      points.push({ at, cpu: 22 + 14 * day + 6 * noise, mem: sv.mem * (sv.use + 0.04 * day + 0.02 * noise), mem_total: sv.mem,
        disk: sv.diskTotal * sv.disk / 100 + (at - end) / 86_400_000 * 1.2e9, disk_total: sv.diskTotal, load: 0.6 + 0.3 * day,
        net_rx: net, net_tx: net === null ? null : net * 0.32 + 6e4 * noise });
    }
    else {
      const req = Math.max(0, Math.round((hours ? 60 : 1) * (420 + 260 * day + 60 * noise)));
      points.push({ at, cpu: hours ? busiest(at, salt) : appCpu(at, salt), mem: 210e6 + 30e6 * day, req, err5: Math.round(req * (noise > 0.25 ? 0.02 : 0.002)), p95_ms: Math.round(140 + 60 * day + 40 * noise) });
    }
  }
  return { range, step_s: step / 1000, points };
}

function servicesOn(name: string) {
  const at = Math.floor(now() / 60_000) * 60_000;
  return services.filter((v) => !v.stopped && envOf(v.project, v.env)?.server === name).map((v) => {
    const salt = [...v.id].reduce((a, c) => a + c.charCodeAt(0), 0);
    const limit = memBytes(v.form.memory);
    return { project: v.project, env: v.env, name: v.form.name, cpu: appCpu(at, salt), cpu_limit: v.form.cpu, mem_limit: limit,
      mem: limit ? limit * (name === "fsn1" ? 0.94 : 0.55) : (v.form.kind === "postgres" ? 380e6 : 160e6 + (salt % 9) * 12e6) };
  });
}

function addFresh(p: string, e: string, f: ServiceForm, quiet = false): Service {
  const s: Service = { id: id("svc_"), project: p, env: e, form: f, deploys: [] };
  services.push(s);
  if (f.kind === "postgres" && !quiet) {
    const pw = Math.random().toString(36).slice(2, 14);
    setVar(p, e, f.name, "PASSWORD", pw);
    setVar(p, e, f.name, "URL", `postgres://app:${pw}@${f.name}:5432/app`);
  }
  return s;
}

function diff(a: ServiceForm, b: ServiceForm): string {
  const out: string[] = [];
  const show = (v: unknown, other: unknown): string => {
    const x = v ?? (Array.isArray(other) ? [] : typeof other === "boolean" ? false : typeof other === "number" ? 0 : "");
    return Array.isArray(x) ? `[${x.map((e) => (typeof e === "string" ? e : JSON.stringify(e))).join(" ")}]` : typeof x === "string" ? x : JSON.stringify(x);
  };
  for (const k of Object.keys(b) as (keyof ServiceForm)[]) {
    const from = show(a[k], b[k]), to = show(b[k], a[k]);
    if (from !== to) out.push(`${k} ${from} -> ${to}`);
  }
  return out.join(", ") || "nothing changes";
}

function putVar(method: string, p: string, e: string, service: string, key: string, body: any): Res {
  const i = vars.findIndex((v) => v.project === p && v.env === e && v.service === service && v.key === key);
  if (method === "DELETE") return i >= 0 && vars.splice(i, 1), ok();
  const v: Var = { project: p, env: e, service, key, build: !!body.build, updated: now(),
    ...(body.ref ? { ref: body.ref } : { agentBox: body.agent_box, recoveryBox: body.recovery_box }) };
  if (i >= 0) vars[i] = v; else vars.push(v);
  return ok();
}

const LOG_LINES = [
  "GET /api/invoices?page=2 200 18ms",
  "GET /health 200 1ms",
  "POST /api/checkout 201 142ms",
  "GET /api/me 200 9ms",
  "worker: sent 3 receipts",
  "GET /static/app.4f1c.js 304 2ms",
  "POST /api/login 401 31ms",
  "error: payment provider timed out after 10s; retrying",
];
const CRAWLERS = [
  { name: "GPTBot", company: "OpenAI", n: 3120, top: "/blog/*" },
  { name: "ClaudeBot", company: "Anthropic", n: 1840, top: "/docs/*" },
  { name: "PerplexityBot", company: "Perplexity", n: 1055, top: "/pricing" },
  { name: "Bytespider", company: "ByteDance", n: 610, top: "/" },
  { name: "CCBot", company: "Common Crawl", n: 290, top: "/blog/*" },
];
let logAlerts: unknown = { rules: [{ id: "r1", text: "payment provider timed out", lines: 5, minutes: 10 }] };
const blockedCrawlers = new Set(["Bytespider"]);
const SAFARI = "Mozilla/5.0 (Macintosh; Intel Mac OS X 14_6) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Safari/605.1.15";
const REQUESTS: [number, string, string, number, number, number, string, string, string, string[]?][] = [
  [0, "GET", "/api/cart", 200, 38, 1200, "Human · DE", "human", SAFARI],
  [22, "GET", "/blog/self-hosting", 200, 12, 18000, "GPTBot · AI", "ai", "Mozilla/5.0 AppleWebKit/537.36 (KHTML, like Gecko; compatible; GPTBot/1.2; +https://openai.com/gptbot)"],
  [431, "POST", "/api/checkout", 500, 1204, 312, "Human · UZ", "human", SAFARI, ["checkout: charging order 9921", "ERROR charge failed: context deadline exceeded"]],
  [910, "GET", "/assets/app-3f9c.js", 304, 2, 0, "Human · US", "human", SAFARI],
  [1535, "GET", "/wp-login.php", 404, 3, 146, "Unknown bot", "bot", "python-requests/2.31"],
  [2292, "GET", "/api/products?page=2", 200, 64, 8400, "Human · UZ", "human", SAFARI],
  [2679, "POST", "/api/session", 201, 91, 420, "Human · KZ", "human", SAFARI],
  [3402, "GET", "/docs/deploy", 200, 15, 22000, "ClaudeBot · AI", "ai", "Mozilla/5.0 AppleWebKit/537.36 (KHTML, like Gecko; compatible; ClaudeBot/1.0; +claudebot@anthropic.com)"],
  [3860, "GET", "/api/cart", 502, 30002, 0, "Human · DE", "human", SAFARI, ["cart: loading session", "ERROR upstream closed the connection"]],
  [4512, "GET", "/health", 200, 1, 2, "Uptime monitor", "bot", "UptimeRobot/2.0"],
  [5000, "GET", "/", 200, 21, 31000, "Googlebot", "bot", "Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)"],
];
function requestRows() {
  const t = now();
  return REQUESTS.map(([ago, method, path, status, ms, size, by, who, ua, lines], i) => ({
    id: (0x7f3a91c2 + i * 7919).toString(16), at: new Date(t - ago).toISOString(), method, path, status, ms, bytes: size, by, who,
    host: "acme.dev", ip: `203.0.113.${24 + i}`, ua, req_bytes: method === "GET" ? 0 : 512, type: path.endsWith(".js") ? "text/javascript" : path.startsWith("/api") ? "application/json" : "text/html",
    replica: `web-${1 + (i % 2)} on hel1`, deploy: "#148 · a1b2c3d", upstream_ms: Math.max(0, ms - 6),
    logs: (lines ?? []).map((text, j) => ({ Time: new Date(t - ago - ms + j * (ms - 2)).toISOString(), Text: `${text} request_id=${(0x7f3a91c2 + i * 7919).toString(16)}` })),
  }));
}

function requests(q: URLSearchParams) {
  const rows = requestRows().filter((r) => (!q.get("status") || String(r.status)[0] === q.get("status")![0]) && (!q.get("method") || r.method === q.get("method"))
    && (!q.get("who") || r.who === q.get("who")) && (!q.get("slower") || r.ms > Number(q.get("slower"))));
  return { count: 12480, s5: 0.8, p50: 38, p95: 412, slowest: "POST /api/checkout", rows };
}

let logSeq = 0;
function logs(s: Service, q: URLSearchParams) {
  const since = q.get("since"), text = (q.get("q") ?? "").toLowerCase();
  const d = current(s);
  const before = s.deploys.filter((x) => x !== d && !x.fail && x.created < (d?.created ?? 0)).at(-1);
  const make = (t: number, from = d) => ({ Time: new Date(t).toISOString(), Deploy: from?.id ?? "", Text: LOG_LINES[(logSeq++ + t) % LOG_LINES.length] });
  const out = since ? [make(now())] : Array.from({ length: 60 }, (_, i) => make(now() - (60 - i) * 7000, i < 20 && before ? before : d));
  if (!since && s.form.kind === "web") out.push(...requestRows().flatMap((r) => r.logs.map((l) => ({ ...l, Deploy: d?.id ?? "" }))));
  out.sort((a, b) => a.Time.localeCompare(b.Time));
  return out.filter((l) => l.Text.toLowerCase().includes(text));
}

const realFetch = window.fetch.bind(window);
window.fetch = async (input: RequestInfo | URL, init?: RequestInit) => {
  const u = new URL(typeof input === "string" ? input : input instanceof URL ? input.href : input.url, location.href);
  if (!u.pathname.startsWith("/api/")) return realFetch(input, init);
  await new Promise((r) => setTimeout(r, 120 + Math.random() * 180));
  let body: unknown;
  try { body = init?.body ? JSON.parse(String(init.body)) : undefined; } catch { body = undefined; }
  let r: Res;
  try { r = handle((init?.method ?? "GET").toUpperCase(), u.pathname, u.searchParams, body ?? {}); }
  catch (e) { r = err(500, String(e)); }
  const text = r.text ?? (r.body === undefined ? "" : JSON.stringify(r.body));
  return new Response(r.status === 204 ? null : text, { status: r.status, headers: { "Content-Type": r.type ?? (r.text ? "text/plain" : "application/json") } });
};

const RealXHR = window.XMLHttpRequest;
class DemoXHR extends RealXHR {
  private target = { method: "GET", url: "" };
  open(method: string, url: string | URL, ...rest: [boolean?, string?, string?]) {
    this.target = { method: method.toUpperCase(), url: String(url) };
    super.open(method, url, rest[0] ?? true, rest[1], rest[2]);
  }
  send(data?: Document | XMLHttpRequestBodyInit | null) {
    const u = new URL(this.target.url, location.href);
    if (!u.pathname.startsWith("/api/")) return super.send(data);
    setTimeout(() => {
      let r: Res;
      try { r = handle(this.target.method, u.pathname, u.searchParams, {}); } catch (e) { r = err(500, String(e)); }
      const text = r.text ?? (r.body === undefined ? "" : JSON.stringify(r.body));
      Object.defineProperties(this, { status: { value: r.status }, responseText: { value: text }, statusText: { value: "" } });
      this.upload.onprogress?.call(this, new ProgressEvent("progress", { lengthComputable: true, loaded: 1, total: 1 }));
      this.onload?.(new ProgressEvent("load"));
    }, 400);
  }
}
window.XMLHttpRequest = DemoXHR;

class FakeEventSource {
  onmessage: ((e: MessageEvent) => void) | null = null;
  onerror: ((e: Event) => void) | null = null;
  private t: number;
  constructor(url: string) {
    const depId = url.match(/\/api\/deploys\/([^/]+)\/stream/)?.[1];
    const tick = () => {
      const d = services.flatMap((s) => s.deploys).find((v) => v.id === depId);
      if (!d) return this.onerror?.(new Event("error"));
      this.onmessage?.(new MessageEvent("message", { data: JSON.stringify(deployView(d)) }));
    };
    this.t = window.setInterval(tick, 500);
    setTimeout(tick, 50);
  }
  close() { clearInterval(this.t); }
}
(window as unknown as { EventSource: unknown }).EventSource = FakeEventSource;

function memBytes(text: string): number {
  const m = /^(\d+(?:\.\d+)?)\s*(mb|gb)$/i.exec(text.trim());
  return m ? Math.round(Number(m[1]) * (m[2].toLowerCase() === "gb" ? 1e9 : 1e6)) : 0;
}

type TemplateService = Partial<ServiceForm> & { name: string; kind: string };
const TEMPLATES: { id: string; title: string; line: string; category: string; new?: boolean; memory: string; icons: string[]; note: string; services: TemplateService[]; vars: { service: string; key: string; kind: string; value?: string; label?: string }[] }[] = [
  { id: "grafana-loki", category: "analytics", title: "Grafana and Loki", line: "Dashboards, with Loki to keep logs.", memory: "1GB", icons: [grafanaIcon, lokiIcon],
    note: "Log in as admin with the password shown below; it is kept in grafana's Environment. Add Loki as a data source at http://loki:3100.",
    services: [{ name: "grafana", kind: "web", image: "grafana/grafana:11.6", repo: "", branch: "", port: 3000, volumes: [{ name: "data", path: "/var/lib/grafana" }], memory: "512MB", auto_deploy: false },
      { name: "loki", kind: "worker", image: "grafana/loki:3", repo: "", branch: "", port: 0, volumes: [{ name: "data", path: "/loki" }], memory: "512MB", auto_deploy: false }],
    vars: [{ service: "grafana", key: "GF_SECURITY_ADMIN_USER", kind: "fixed", value: "admin" }, { service: "grafana", key: "GF_SECURITY_ADMIN_PASSWORD", kind: "generate" }] },
  { id: "n8n", category: "automation", title: "n8n", line: "Workflows that connect your apps.", memory: "512MB", icons: [n8nIcon], note: "Open it and create the owner account.",
    services: [{ name: "n8n", kind: "web", image: "n8nio/n8n:stable", repo: "", branch: "", port: 5678, volumes: [{ name: "data", path: "/home/node/.n8n" }], memory: "512MB", auto_deploy: false }],
    vars: [{ service: "n8n", key: "N8N_ENCRYPTION_KEY", kind: "generate" }] },
  { id: "umami", category: "analytics", title: "Umami", line: "Website analytics without cookies.", memory: "1GB", icons: [umamiIcon], note: "Log in as admin with the password umami, then change it at once.",
    services: [{ name: "umami-db", kind: "postgres", memory: "512MB" }, { name: "umami", kind: "web", image: "ghcr.io/umami-software/umami:postgresql-latest", repo: "", branch: "", port: 3000, memory: "512MB", auto_deploy: false }],
    vars: [{ service: "umami", key: "DATABASE_URL", kind: "ref", value: "umami-db.url" }, { service: "umami", key: "APP_SECRET", kind: "generate" }] },
  { id: "uptime-kuma", category: "tools", title: "Uptime Kuma", line: "Watches your sites and shows a status page.", memory: "256MB", icons: [uptimeIcon], note: "Open it and create the first user; that user is the admin.",
    services: [{ name: "uptime-kuma", kind: "web", image: "louislam/uptime-kuma:1", repo: "", branch: "", port: 3001, volumes: [{ name: "data", path: "/app/data" }], memory: "256MB", auto_deploy: false }],
    vars: [] },
  { id: "pocketbase", title: "PocketBase", category: "backend", new: true, line: "Auth, a realtime database, file storage and an admin UI in one Go binary. Its data is SQLite in a volume.", memory: "256MB", icons: [pocketbaseIcon],
    note: "Open /_/ on its address and sign in as the admin email with the password shown below; it is kept in pocketbase's Environment.",
    services: [{ name: "pocketbase", kind: "web", image: "ghcr.io/muchobien/pocketbase:0.42.3", repo: "", branch: "", port: 8090, volumes: [{ name: "data", path: "/pb_data" }], memory: "256MB", auto_deploy: false }],
    vars: [{ service: "pocketbase", key: "PB_ADMIN_EMAIL", kind: "ask", label: "Admin email" }, { service: "pocketbase", key: "PB_ADMIN_PASSWORD", kind: "generate" }] },
  { id: "appwrite", title: "Appwrite", category: "backend", new: true, line: "Auth, databases, storage, functions and messaging. Runs Appwrite's own self-hosting compose file.", memory: "4GB", icons: [appwriteIcon],
    note: "Open it and create the first account; that account owns the console.",
    services: [{ name: "appwrite-db", kind: "mariadb", memory: "1GB" }, { name: "appwrite-redis", kind: "valkey", memory: "256MB" },
      { name: "appwrite", kind: "web", image: "appwrite/appwrite:1.8.0", repo: "", branch: "", port: 80, volumes: [{ name: "uploads", path: "/storage/uploads" }, { name: "functions", path: "/storage/functions" }], memory: "1GB", auto_deploy: false },
      { name: "appwrite-realtime", kind: "worker", image: "appwrite/appwrite:1.8.0", repo: "", branch: "", port: 0, memory: "512MB", auto_deploy: false },
      { name: "appwrite-worker-functions", kind: "worker", image: "appwrite/appwrite:1.8.0", repo: "", branch: "", port: 0, memory: "512MB", auto_deploy: false },
      { name: "appwrite-worker-builds", kind: "worker", image: "appwrite/appwrite:1.8.0", repo: "", branch: "", port: 0, memory: "512MB", auto_deploy: false }],
    vars: [{ service: "appwrite", key: "_APP_OPENSSL_KEY_V1", kind: "generate" }] },
  { id: "supabase", title: "Supabase", category: "backend", new: true, line: "Postgres with auth, storage, realtime, a REST API and Studio. Runs Supabase's self-hosting compose file.", memory: "4GB", icons: [supabaseIcon],
    note: "Open Studio through kong's address and sign in with the dashboard password shown below.",
    services: [{ name: "supabase-db", kind: "postgres", memory: "1GB" },
      { name: "kong", kind: "web", image: "kong:2.8.1", repo: "", branch: "", port: 8000, memory: "256MB", auto_deploy: false },
      { name: "auth", kind: "worker", image: "supabase/gotrue:v2.180.0", repo: "", branch: "", port: 0, memory: "256MB", auto_deploy: false },
      { name: "rest", kind: "worker", image: "postgrest/postgrest:v12.2.12", repo: "", branch: "", port: 0, memory: "256MB", auto_deploy: false },
      { name: "realtime", kind: "worker", image: "supabase/realtime:v2.51.11", repo: "", branch: "", port: 0, memory: "512MB", auto_deploy: false },
      { name: "storage", kind: "worker", image: "supabase/storage-api:v1.28.0", repo: "", branch: "", port: 0, volumes: [{ name: "files", path: "/var/lib/storage" }], memory: "256MB", auto_deploy: false },
      { name: "studio", kind: "worker", image: "supabase/studio:2025.10.01", repo: "", branch: "", port: 0, memory: "512MB", auto_deploy: false }],
    vars: [{ service: "supabase-db", key: "POSTGRES_PASSWORD", kind: "generate" }, { service: "kong", key: "DASHBOARD_PASSWORD", kind: "generate" }] },
];
