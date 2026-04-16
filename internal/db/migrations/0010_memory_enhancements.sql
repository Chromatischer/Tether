ALTER TABLE memory_items ADD COLUMN pinned       INTEGER NOT NULL DEFAULT 0;
ALTER TABLE memory_items ADD COLUMN expires_at   INTEGER;
ALTER TABLE memory_items ADD COLUMN last_used_at INTEGER;
