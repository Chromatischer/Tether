package store

import (
	"database/sql"
	"errors"
	"time"
)

func FirstConversationID(db *sql.DB, userID int64) (int64, bool, error) {
	var id int64
	err := db.QueryRow(`SELECT id FROM conversations WHERE user_id=? ORDER BY id ASC LIMIT 1`, userID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return id, true, nil
}

func LatestUserMessageTime(db *sql.DB, userID int64) (time.Time, bool, error) {
	var created string
	err := db.QueryRow(`
SELECT m.created_at
FROM messages m
JOIN conversations c ON c.id = m.conversation_id
WHERE c.user_id = ? AND m.role = 'user'
ORDER BY m.id DESC
LIMIT 1
`, userID).Scan(&created)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, err
	}
	// created_at is stored as sqlite default iso string.
	t, err := time.Parse(time.RFC3339Nano, created)
	if err != nil {
		// best-effort: treat as now
		return time.Now(), true, nil
	}
	return t, true, nil
}
