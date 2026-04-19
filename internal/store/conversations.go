package store

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"
)

const activeConversationSettingKey = "active_conversation_id"

func GetOrCreateDefaultConversation(db *sql.DB, userID int64) (*Conversation, error) {
	var c Conversation
	row := db.QueryRow(`SELECT id, user_id, COALESCE(title,'') FROM conversations WHERE user_id = ? ORDER BY id ASC LIMIT 1`, userID)
	err := row.Scan(&c.ID, &c.UserID, &c.Title)
	if err == nil {
		return &c, nil
	}
	if err != sql.ErrNoRows {
		return nil, err
	}
	res, err := db.Exec(`INSERT INTO conversations(user_id, title) VALUES (?, ?)`, userID, "")
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return &Conversation{ID: id, UserID: userID, Title: ""}, nil
}

func CreateConversation(db *sql.DB, userID int64, title string) (*Conversation, error) {
	res, err := db.Exec(`INSERT INTO conversations(user_id, title) VALUES (?, ?)`, userID, title)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return &Conversation{ID: id, UserID: userID, Title: title}, nil
}

func GetConversation(db *sql.DB, userID, conversationID int64) (*Conversation, bool, error) {
	var c Conversation
	row := db.QueryRow(`SELECT id, user_id, COALESCE(title,'') FROM conversations WHERE id=? AND user_id=?`, conversationID, userID)
	err := row.Scan(&c.ID, &c.UserID, &c.Title)
	if err == sql.ErrNoRows {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return &c, true, nil
}

func GetOrCreateActiveConversation(db *sql.DB, userID int64) (*Conversation, error) {
	if raw, ok, err := GetUserSetting(db, userID, activeConversationSettingKey); err == nil && ok {
		if convID, parseErr := strconv.ParseInt(strings.TrimSpace(raw), 10, 64); parseErr == nil && convID > 0 {
			if conv, found, getErr := GetConversation(db, userID, convID); getErr != nil {
				return nil, getErr
			} else if found {
				return conv, nil
			}
		}
	} else if err != nil {
		return nil, err
	}

	conv, err := GetOrCreateDefaultConversation(db, userID)
	if err != nil {
		return nil, err
	}
	if err := SetActiveConversation(db, userID, conv.ID); err != nil {
		return nil, err
	}
	return conv, nil
}

func SetActiveConversation(db *sql.DB, userID, conversationID int64) error {
	if _, ok, err := GetConversation(db, userID, conversationID); err != nil {
		return err
	} else if !ok {
		return fmt.Errorf("conversation not found")
	}
	return SetUserSetting(db, userID, activeConversationSettingKey, strconv.FormatInt(conversationID, 10))
}

func EncodeResumeCode(conversationID int64) string {
	return "r" + strings.ToLower(strconv.FormatInt(conversationID, 36))
}

func DecodeResumeCode(code string) (int64, error) {
	code = strings.TrimSpace(strings.ToLower(code))
	if code == "" {
		return 0, fmt.Errorf("resume code required")
	}
	code = strings.TrimPrefix(code, "r")
	convID, err := strconv.ParseInt(code, 36, 64)
	if err != nil || convID <= 0 {
		return 0, fmt.Errorf("invalid resume code")
	}
	return convID, nil
}

func ListRecentMessages(db *sql.DB, conversationID int64, limit int) ([]Message, error) {
	rows, err := db.Query(`SELECT id, conversation_id, role, content, created_at FROM messages WHERE conversation_id = ? ORDER BY id DESC LIMIT ?`, conversationID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Message{}
	for rows.Next() {
		var m Message
		if err := rows.Scan(&m.ID, &m.ConversationID, &m.Role, &m.Content, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// reverse to chronological
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

func LatestMessageID(db *sql.DB, conversationID int64) (id int64, ok bool, err error) {
	err = db.QueryRow(`SELECT COALESCE(MAX(id),0) FROM messages WHERE conversation_id = ?`, conversationID).Scan(&id)
	if err != nil {
		return 0, false, err
	}
	if id <= 0 {
		return 0, false, nil
	}
	return id, true, nil
}

func ListRecentMessagesBeforeID(db *sql.DB, conversationID int64, beforeOrEqualID int64, limit int) ([]Message, error) {
	if beforeOrEqualID <= 0 {
		return ListRecentMessages(db, conversationID, limit)
	}
	rows, err := db.Query(`SELECT id, conversation_id, role, content, created_at FROM messages WHERE conversation_id = ? AND id <= ? ORDER BY id DESC LIMIT ?`, conversationID, beforeOrEqualID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Message{}
	for rows.Next() {
		var m Message
		if err := rows.Scan(&m.ID, &m.ConversationID, &m.Role, &m.Content, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// reverse to chronological
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

func AddMessage(db *sql.DB, conversationID int64, role, content string) error {
	_, err := db.Exec(`INSERT INTO messages(conversation_id, role, content) VALUES (?, ?, ?)`, conversationID, role, content)
	return err
}
