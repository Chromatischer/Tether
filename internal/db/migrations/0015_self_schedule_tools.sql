-- Add tool snapshot to self_schedules so scheduled runs can execute with the
-- same tool access as when they were created.

ALTER TABLE self_schedules ADD COLUMN active_tools_json TEXT NOT NULL DEFAULT '[]';

CREATE INDEX IF NOT EXISTS idx_self_schedules_due2 ON self_schedules(user_id, run_at, claimed_at, done_at, canceled_at);
