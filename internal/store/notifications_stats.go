package store

import (
	"database/sql"
	"errors"
	"time"
)

func CountNotificationsSince(db *sql.DB, userID int64, since time.Time) (int, error) {
	var c int
	err := db.QueryRow(`SELECT COUNT(1) FROM notifications WHERE user_id=? AND created_at >= ?`, userID, since.Unix()).Scan(&c)
	return c, err
}

func LatestNotificationTime(db *sql.DB, kind string) (time.Time, bool, error) {
	var created int64
	err := db.QueryRow(`SELECT created_at FROM notifications WHERE kind=? ORDER BY id DESC LIMIT 1`, kind).Scan(&created)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, err
	}
	return time.Unix(created, 0), true, nil
}

func LatestNotificationTimeForUser(db *sql.DB, userID int64, kind string) (time.Time, bool, error) {
	var created int64
	err := db.QueryRow(`SELECT created_at FROM notifications WHERE user_id=? AND kind=? ORDER BY id DESC LIMIT 1`, userID, kind).Scan(&created)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, err
	}
	return time.Unix(created, 0), true, nil
}
