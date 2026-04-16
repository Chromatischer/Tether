-- Web fetch cache: stores truncated HTTP bodies for later summarization.
-- Note: may include sensitive content if fetched with authenticated headers.

CREATE TABLE IF NOT EXISTS web_fetch_cache (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id INTEGER NOT NULL,
  cache_key TEXT NOT NULL,
  url TEXT NOT NULL,
  status INTEGER NOT NULL,
  content_type TEXT NOT NULL,
  fetched_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  body BLOB NOT NULL,
  truncated INTEGER NOT NULL DEFAULT 0,
  bytes INTEGER NOT NULL,
  UNIQUE(user_id, cache_key)
);

CREATE INDEX IF NOT EXISTS idx_web_fetch_cache_user_time ON web_fetch_cache(user_id, fetched_at);
