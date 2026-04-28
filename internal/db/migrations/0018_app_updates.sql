CREATE TABLE IF NOT EXISTS app_update_settings (
  id INTEGER PRIMARY KEY CHECK (id = 1),
  auto_enabled INTEGER NOT NULL DEFAULT 0,
  source_mode TEXT NOT NULL DEFAULT 'release',
  branch TEXT NOT NULL DEFAULT 'main',
  schedule_utc TEXT NOT NULL DEFAULT '03:00',
  command TEXT NOT NULL DEFAULT 'sudo -n /usr/local/bin/tether-update',
  last_checked_at INTEGER,
  last_available_ref TEXT,
  last_successful_ref TEXT,
  last_run_at INTEGER,
  updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS app_update_runs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  source_mode TEXT NOT NULL,
  source_ref TEXT,
  status TEXT NOT NULL,
  output TEXT,
  started_at INTEGER NOT NULL,
  finished_at INTEGER
);

CREATE INDEX IF NOT EXISTS idx_app_update_runs_started_at ON app_update_runs(started_at);
