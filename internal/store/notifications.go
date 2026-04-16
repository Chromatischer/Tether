package store

import (
	"database/sql"
	"time"
)

type Notification struct {
	ID        int64
	UserID    int64
	Kind      string
	Content   string
	CreatedAt time.Time
}

func AddNotification(db *sql.DB, userID int64, kind, content string) error {
	now := time.Now().Unix()
	_, err := db.Exec(`INSERT INTO notifications(user_id, kind, content, created_at) VALUES (?,?,?,?)`, userID, kind, content, now)
	return err
}

// AddNotificationDelivered inserts a notification but marks it as already delivered.
// Useful for logging one-off proactive outputs without re-injecting them into the chat on next login.
func AddNotificationDelivered(db *sql.DB, userID int64, kind, content string) error {
	now := time.Now().Unix()
	_, err := db.Exec(`INSERT INTO notifications(user_id, kind, content, created_at, delivered_at) VALUES (?,?,?,?,?)`, userID, kind, content, now, now)
	return err
}

func ListUndeliveredNotifications(db *sql.DB, userID int64, limit int) ([]Notification, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := db.Query(`SELECT id, user_id, kind, content, created_at FROM notifications WHERE user_id=? AND delivered_at IS NULL ORDER BY id ASC LIMIT ?`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Notification{}
	for rows.Next() {
		var n Notification
		var created int64
		if err := rows.Scan(&n.ID, &n.UserID, &n.Kind, &n.Content, &created); err != nil {
			return nil, err
		}
		n.CreatedAt = time.Unix(created, 0)
		out = append(out, n)
	}
	return out, rows.Err()
}

func MarkNotificationDelivered(db *sql.DB, id int64) error {
	_, err := db.Exec(`UPDATE notifications SET delivered_at=? WHERE id=?`, time.Now().Unix(), id)
	return err
}

func HasNotificationSince(db *sql.DB, userID int64, kind string, since time.Time) (bool, error) {
	var c int
	err := db.QueryRow(`SELECT COUNT(1) FROM notifications WHERE user_id=? AND kind=? AND created_at >= ?`, userID, kind, since.Unix()).Scan(&c)
	if err != nil {
		return false, err
	}
	return c > 0, nil
}

func ListUserIDs(db *sql.DB) ([]int64, error) {
	rows, err := db.Query(`SELECT id FROM users ORDER BY id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
