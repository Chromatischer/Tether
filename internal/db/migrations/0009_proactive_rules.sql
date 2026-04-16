CREATE TABLE IF NOT EXISTS proactive_rules (
  user_id INTEGER PRIMARY KEY,
  rules_yaml TEXT NOT NULL,
  updated_at INTEGER NOT NULL,
  FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_proactive_rules_updated_at ON proactive_rules(updated_at);
