package store

import (
	"database/sql"
)

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

func AddMessage(db *sql.DB, conversationID int64, role, content string) error {
	_, err := db.Exec(`INSERT INTO messages(conversation_id, role, content) VALUES (?, ?, ?)`, conversationID, role, content)
	return err
}
