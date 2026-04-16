CREATE TABLE IF NOT EXISTS signal_link_codes (
  code TEXT PRIMARY KEY,
  user_id INTEGER NOT NULL,
  created_at INTEGER NOT NULL,
  expires_at INTEGER NOT NULL,
  FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_signal_link_codes_expires_at ON signal_link_codes(expires_at);
