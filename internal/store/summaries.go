package store

import (
	"database/sql"
	"errors"
	"time"
)

type ConversationSummary struct {
	Summary                    string
	UpdatedAt                  time.Time
	SummarizedThroughMessageID int64
}

func GetConversationSummaryState(db *sql.DB, conversationID int64) (ConversationSummary, bool, error) {
	var s ConversationSummary
	var upd int64
	err := db.QueryRow(`SELECT summary, updated_at, COALESCE(summarized_through_message_id, 0) FROM conversation_summaries WHERE conversation_id=?`, conversationID).
		Scan(&s.Summary, &upd, &s.SummarizedThroughMessageID)
	if errors.Is(err, sql.ErrNoRows) {
		return ConversationSummary{}, false, nil
	}
	if err != nil {
		return ConversationSummary{}, false, err
	}
	s.UpdatedAt = time.Unix(upd, 0)
	return s, true, nil
}

func GetConversationSummary(db *sql.DB, conversationID int64) (summary string, updatedAt time.Time, ok bool, err error) {
	s, ok, err := GetConversationSummaryState(db, conversationID)
	if err != nil || !ok {
		return "", time.Time{}, ok, err
	}
	return s.Summary, s.UpdatedAt, true, nil
}

func SetConversationSummary(db *sql.DB, conversationID int64, summary string) error {
	return SetConversationSummaryState(db, conversationID, summary, 0)
}

func SetConversationSummaryState(db *sql.DB, conversationID int64, summary string, throughMessageID int64) error {
	_, err := db.Exec(`INSERT OR REPLACE INTO conversation_summaries(conversation_id, summary, updated_at, summarized_through_message_id) VALUES (?,?,?,?)`,
		conversationID, summary, time.Now().Unix(), throughMessageID)
	return err
}

func CountMessages(db *sql.DB, conversationID int64) (int64, error) {
	var c int64
	err := db.QueryRow(`SELECT COUNT(1) FROM messages WHERE conversation_id=?`, conversationID).Scan(&c)
	return c, err
}
