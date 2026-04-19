-- Self-scheduled proactive runs + conversation-scoped notifications.

-- Add optional conversation_id to notifications so proactive messages can be injected
-- into the correct conversation thread.
-- Existing code paths that don't set it will leave it NULL (treated as "active conversation").
ALTER TABLE notifications ADD COLUMN conversation_id INTEGER;
CREATE INDEX IF NOT EXISTS idx_notifications_user_conv_undelivered ON notifications(user_id, conversation_id, delivered_at);

-- Self-schedules are one-off timers created by the main chat agent via the self.schedule tool.
-- They are executed by the proactive scheduler.
CREATE TABLE IF NOT EXISTS self_schedules (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id INTEGER NOT NULL,
  conversation_id INTEGER NOT NULL,
  anchor_message_id INTEGER NOT NULL,
  prompt TEXT NOT NULL,
  run_at INTEGER NOT NULL,
  created_at INTEGER NOT NULL,
  claimed_at INTEGER,
  done_at INTEGER,
  canceled_at INTEGER,
  error TEXT,
  FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_self_schedules_due ON self_schedules(user_id, run_at, done_at, canceled_at, claimed_at);
