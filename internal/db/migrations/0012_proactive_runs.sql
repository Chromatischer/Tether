CREATE TABLE IF NOT EXISTS proactive_runs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id INTEGER NOT NULL,
  agent_id TEXT NOT NULL,
  trigger TEXT NOT NULL,
  day_key TEXT NOT NULL, -- YYYY-MM-DD (UTC)
  created_at INTEGER NOT NULL,
  FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE,
  UNIQUE(user_id, agent_id, trigger, day_key)
);

CREATE INDEX IF NOT EXISTS idx_proactive_runs_user_day ON proactive_runs(user_id, day_key);
CREATE INDEX IF NOT EXISTS idx_proactive_runs_user_agent_created ON proactive_runs(user_id, agent_id, created_at);
