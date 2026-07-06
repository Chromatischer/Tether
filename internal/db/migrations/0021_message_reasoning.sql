-- Persist the provider's signed/encrypted reasoning for an assistant message so
-- it can be replayed on later turns. Keyed by the assistant message it belongs
-- to; `model` pins the producing model so we never replay a block to a model
-- that cannot verify its signature.
CREATE TABLE IF NOT EXISTS message_reasoning (
  message_id INTEGER PRIMARY KEY,
  conversation_id INTEGER NOT NULL,
  model TEXT NOT NULL,
  blocks_json TEXT NOT NULL,
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  FOREIGN KEY(message_id) REFERENCES messages(id) ON DELETE CASCADE,
  FOREIGN KEY(conversation_id) REFERENCES conversations(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_message_reasoning_conversation
  ON message_reasoning(conversation_id);
