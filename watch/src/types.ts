export interface Env {
  DB: D1Database;
  PANEL_KEY: string;
  BOT_TOKEN: string;
  CHAT_ID: string;
  TELEGRAM_API: string;
  RELAY_URL?: string;
  RELAY_KEY?: string;
}

export type Kind = "url" | "server" | "panel" | "heartbeat" | "backup" | "cron";

export interface Expectation {
  id: string;
  kind: Kind;
  subject: string;
  label: string;
  target: string;
  server: string;
  period_s: number;
  grace_s: number;
  public: boolean;
}

export interface Incident {
  id: string;
  subject: string;
  kind: string;
  label: string;
  source: "watch" | "panel";
  opened_at: number;
  closed_at: number | null;
  cause: string;
  note: string;
  public: number;
}

export const MINUTE = 60_000;
export const SILENT_AFTER = 3 * MINUTE;
