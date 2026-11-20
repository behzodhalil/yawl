CREATE TABLE IF NOT EXISTS panels (
  id TEXT PRIMARY KEY,
  key_hash TEXT NOT NULL UNIQUE,
  host TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  win_at INTEGER NOT NULL DEFAULT 0,
  win_n INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS codes (
  code TEXT PRIMARY KEY,
  panel_id TEXT NOT NULL,
  expires_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS chats (
  panel_id TEXT NOT NULL,
  chat_id TEXT NOT NULL,
  type TEXT NOT NULL,
  title TEXT NOT NULL,
  linked_at INTEGER NOT NULL,
  gone_at INTEGER,
  PRIMARY KEY (panel_id, chat_id)
);
CREATE INDEX IF NOT EXISTS chats_chat ON chats(chat_id);
