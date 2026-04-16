CREATE TABLE IF NOT EXISTS discord_link_codes (
  code TEXT PRIMARY KEY,
  discord_user_id TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  expires_at INTEGER NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_discord_link_codes_discord_user_id
  ON discord_link_codes(discord_user_id);

CREATE INDEX IF NOT EXISTS idx_discord_link_codes_expires_at
  ON discord_link_codes(expires_at);
