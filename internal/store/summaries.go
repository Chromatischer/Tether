package store

import (
	"database/sql"
	"errors"
	"time"
)

func GetConversationSummary(db *sql.DB, conversationID int64) (summary string, updatedAt time.Time, ok bool, err error) {
	var s string
	var upd int64
	err = db.QueryRow(`SELECT summary, updated_at FROM conversation_summaries WHERE conversation_id=?`, conversationID).Scan(&s, &upd)
	if errors.Is(err, sql.ErrNoRows) {
		return "", time.Time{}, false, nil
	}
	if err != nil {
		return "", time.Time{}, false, err
	}
	return s, time.Unix(upd, 0), true, nil
}

func SetConversationSummary(db *sql.DB, conversationID int64, summary string) error {
	_, err := db.Exec(`INSERT OR REPLACE INTO conversation_summaries(conversation_id, summary, updated_at) VALUES (?,?,?)`, conversationID, summary, time.Now().Unix())
	return err
}

func CountMessages(db *sql.DB, conversationID int64) (int64, error) {
	var c int64
	err := db.QueryRow(`SELECT COUNT(1) FROM messages WHERE conversation_id=?`, conversationID).Scan(&c)
	return c, err
}
