package store

import (
	"database/sql"
	"errors"
)

func GetUserSetting(db *sql.DB, userID int64, key string) (string, bool, error) {
	var val string
	err := db.QueryRow(
		`SELECT value FROM user_settings WHERE user_id=? AND key=?`,
		userID, key,
	).Scan(&val)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return val, true, nil
}

func SetUserSetting(db *sql.DB, userID int64, key, value string) error {
	_, err := db.Exec(
		`INSERT OR REPLACE INTO user_settings(user_id, key, value) VALUES (?,?,?)`,
		userID, key, value,
	)
	return err
}

func GetAllUserSettings(db *sql.DB, userID int64) (map[string]string, error) {
	rows, err := db.Query(
		`SELECT key, value FROM user_settings WHERE user_id=?`,
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		m[k] = v
	}
	return m, rows.Err()
}
