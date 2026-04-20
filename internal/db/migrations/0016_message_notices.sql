ALTER TABLE messages ADD COLUMN is_notice INTEGER NOT NULL DEFAULT 0;

UPDATE messages
SET is_notice = 1
WHERE role = 'system';

UPDATE messages
SET is_notice = 1
WHERE role = 'assistant'
  AND (
    lower(trim(content)) LIKE '(agent error)%'
    OR lower(trim(content)) LIKE '(skill error)%'
    OR lower(trim(content)) LIKE 'admin only%'
    OR lower(trim(content)) LIKE 'conversation not found%'
    OR lower(trim(content)) LIKE 'failed%'
    OR lower(trim(content)) LIKE 'invalid%'
    OR lower(trim(content)) LIKE 'memory added%'
    OR lower(trim(content)) LIKE 'memory updated%'
    OR lower(trim(content)) LIKE 'memory deleted%'
    OR lower(trim(content)) LIKE 'pending tool confirmation rejected%'
    OR lower(trim(content)) LIKE 'secret stored%'
    OR lower(trim(content)) LIKE 'secrets unavailable%'
    OR lower(trim(content)) LIKE 'sensitive data detected%'
    OR lower(trim(content)) LIKE 'signal linked%'
    OR lower(trim(content)) LIKE 'signal unlinked%'
    OR lower(trim(content)) LIKE 'signal:%'
    OR lower(trim(content)) LIKE 'discord linked%'
    OR lower(trim(content)) LIKE 'discord unlinked%'
    OR lower(trim(content)) LIKE 'discord:%'
    OR lower(trim(content)) LIKE 'started a fresh conversation%'
    OR lower(trim(content)) LIKE 'spawned subagent%'
    OR lower(trim(content)) LIKE 'task added%'
    OR lower(trim(content)) LIKE 'task updated%'
    OR lower(trim(content)) LIKE 'task marked done%'
    OR lower(trim(content)) LIKE 'the assistant response contained secret-like content and was redacted%'
    OR lower(trim(content)) LIKE 'unknown command%'
    OR lower(trim(content)) LIKE 'updated role%'
    OR lower(trim(content)) LIKE 'usage:%'
  );
