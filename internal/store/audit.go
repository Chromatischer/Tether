package store

import (
	"database/sql"
	"time"
)

func AddAuditEvent(db *sql.DB, userID *int64, typ string, payloadJSON string) error {
	now := time.Now().Unix()
	if userID == nil {
		_, err := db.Exec(`INSERT INTO audit_events(user_id, type, payload_json, created_at) VALUES (NULL, ?, ?, ?)`, typ, payloadJSON, now)
		return err
	}
	_, err := db.Exec(`INSERT INTO audit_events(user_id, type, payload_json, created_at) VALUES (?, ?, ?, ?)`, *userID, typ, payloadJSON, now)
	return err
}
