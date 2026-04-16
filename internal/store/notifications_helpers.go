package store

import "database/sql"

func CountUndeliveredNotifications(db *sql.DB) (int, error) {
	var c int
	err := db.QueryRow(`SELECT COUNT(1) FROM notifications WHERE delivered_at IS NULL`).Scan(&c)
	return c, err
}
