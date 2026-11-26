export type Change = { op: string; kind: string; name: string; detail?: string };
export type StatusRow = {
  project: string; env: string; service: string; kind: string; url?: string;
  deploy_id?: string; state: string; commit?: string; cause?: string; health: string;
  tags?: string[];
  subject?: string; at?: number;
  no_preview?: boolean;
  stopped?: boolean;
};
export type EnvView = { name: string; server: string; services: StatusRow[]; preview?: boolean; preview_of?: string; server_offline?: boolean; server_last_seen?: number };
export type ProjectView = { name: string; envs: EnvView[] };
export type ServerView = {
  forecast?: { days: number | null; gb_per_day: number; growing: boolean };
  id: string; name: string; public_ip: string; agent_version: string;
  last_heartbeat?: number; disk_used_pct: number; offline: boolean;
  private_ip?: string; network?: string; peers?: { name: string; last_handshake: number }[];
  os_name?: string; os_id?: string; kernel?: string; cores?: number; booted_at?: number; mem_total?: number; disk_total?: number; firewall_off?: boolean; reboot_required?: boolean;
};
export type ServiceForm = {
  name: string; kind: string; repo: string; branch: string; port: number; health_path: string;
  health_timeout: number; drain_timeout: number; auto_deploy: boolean; release: string[];
  release_timeout: number; domains: string[]; version: number; backup_at: string;
  outside_port: number; allow: string[];
  schedule: string; timezone: string; command: string[]; timeout: number;
  volumes: Volume[];
  cpu: number; memory: string;
  image: string;
  backup: boolean;
  pitr?: boolean;
  builder?: string;
  root?: string;
  steps?: Steps;
  noindex?: boolean; www?: string;
  watch?: string[]; no_preview?: boolean;
  replicas?: number;
  tags?: string[];
};
export type Volume = { name: string; path: string };
export type Steps = { install?: string; build?: string; start?: string; output?: string };
export type DomainView = { host: string; dns_state: string; dns_detail?: string; cert_state: string };
export type ServiceView = {
  id: string;
  form: ServiceForm; url?: string; health: string; server: string; current_deploy_id?: string; domains: DomainView[]; server_offline?: boolean; server_last_seen?: number;
  memory_limit?: number;
  stopped?: boolean;
  maintenance?: boolean; password_user?: string;
  used_by?: { env: string; service: string }[];
  settings_at?: number;
  skip_words?: string[];
  preview_max?: number;
  preview_idle_days?: number;
};
export type DeployWords = {
  restart?: boolean;
  subject?: string; by?: string; took_ms?: number; live: boolean; can_roll_back: boolean; rollback_target?: boolean; auto_rolled_back?: boolean };
export type DeployRow = { id: string; state: string; commit?: string; digest?: string; cause?: string; rollback: boolean; created_at: number; finished_at?: number } & DeployWords;
export type StepView = { step: string; state: string; output?: string; started_at: number; finished_at?: number };
export type DeployView = {
  id: string; service: string; state: string; cause?: string; commit?: string; digest?: string; rollback: boolean;
  created_at: number; steps: StepView[]; warnings: string[];
} & DeployWords;
export type InspectData = {
  commit: string; dockerfile: boolean; language: string; reads_port: boolean; port: number;
  health_path: string; missing: string[]; warnings: string[];
  builder: string; static: boolean; notes: string[]; framework?: string;
};

export type DiffRow = { service: string; key: string; state: "same" | "different" | "only_left" | "only_right" };
export type SealedRow = { id: string; key: string; service: string; recovery_box: string; updated_at: number };
export type MovePlan = { lines: string[]; to_pubkey: string; recovery_pubkey: string; restore: boolean; vars: SealedRow[] };
export type MoveView = { id: string; from: string; to: string; step: string; state: "running" | "failed" | "done"; cause: string; cleaned: boolean; lines: string[] };

export type CheckResult = { ok: boolean; ms?: number; detail: string; since: number; at?: number };
export type Checks = { at: number; results: Record<string, CheckResult> };
export type Off = { off: true };
export type Incident = {
  id: string; subject: string; kind: string; label: string; source: string;
  opened_at: number; closed_at: number | null; cause: string; note: string;
};
export type WatchState = {
  state: "off" | "on" | "error"; url?: string; error?: string; account?: string; note?: string;
  checks: number; last_run?: number; servers: { name: string; state: string }[];
};
export type Heartbeat = { id: string; name: string; period_s: number; grace_s: number; url: string };

export type CronRun = {
  ID: string; ScheduledAt: number; StartedAt: number; FinishedAt: number; Exit: number;
  Cause: string; Output: string; Skipped: boolean; TimedOut: boolean;
};
export type CronRuns = { next: number; timezone: string; running: boolean; runs: CronRun[] };
