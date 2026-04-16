package store

import (
	"database/sql"
	"errors"
	"time"
)

func LatestAuditEventTime(db *sql.DB, typ string) (time.Time, bool, error) {
	var created int64
	err := db.QueryRow(`SELECT created_at FROM audit_events WHERE type=? ORDER BY id DESC LIMIT 1`, typ).Scan(&created)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, err
	}
	return time.Unix(created, 0), true, nil
}
