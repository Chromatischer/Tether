package store

import (
	"database/sql"
	"time"
)

type AuditEvent struct {
	ID        int64
	UserID    *int64
	Type      string
	Payload   string
	CreatedAt time.Time
}

func ListAuditEvents(db *sql.DB, limit int) ([]AuditEvent, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := db.Query(`SELECT id, user_id, type, COALESCE(payload_json,''), created_at FROM audit_events ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AuditEvent{}
	for rows.Next() {
		var ev AuditEvent
		var uid sql.NullInt64
		var created int64
		if err := rows.Scan(&ev.ID, &uid, &ev.Type, &ev.Payload, &created); err != nil {
			return nil, err
		}
		if uid.Valid {
			ev.UserID = &uid.Int64
		}
		ev.CreatedAt = time.Unix(created, 0)
		out = append(out, ev)
	}
	return out, rows.Err()
}
