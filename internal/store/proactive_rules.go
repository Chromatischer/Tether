package store

import (
	"database/sql"
	"errors"
	"strings"
	"time"
)

// GetProactiveRulesYAML returns the stored proactive rules YAML for a user.
func GetProactiveRulesYAML(db *sql.DB, userID int64) (rulesYAML string, updatedAt time.Time, ok bool, err error) {
	var y string
	var upd int64
	err = db.QueryRow(`SELECT rules_yaml, updated_at FROM proactive_rules WHERE user_id=?`, userID).Scan(&y, &upd)
	if errors.Is(err, sql.ErrNoRows) {
		return "", time.Time{}, false, nil
	}
	if err != nil {
		return "", time.Time{}, false, err
	}
	return y, time.Unix(upd, 0), true, nil
}

// SetProactiveRulesYAML sets the stored proactive rules YAML for a user.
func SetProactiveRulesYAML(db *sql.DB, userID int64, rulesYAML string) error {
	rulesYAML = strings.TrimSpace(rulesYAML)
	if rulesYAML == "" {
		return errors.New("rules_yaml required")
	}
	now := time.Now().Unix()
	_, err := db.Exec(`INSERT OR REPLACE INTO proactive_rules(user_id, rules_yaml, updated_at) VALUES (?,?,?)`, userID, rulesYAML, now)
	return err
}

// EnsureDefaultProactiveRulesYAML inserts default rules if no rules exist yet.
func EnsureDefaultProactiveRulesYAML(db *sql.DB, userID int64, defaultYAML string) error {
	defaultYAML = strings.TrimSpace(defaultYAML)
	if defaultYAML == "" {
		return errors.New("default rules_yaml required")
	}
	now := time.Now().Unix()
	_, err := db.Exec(`INSERT OR IGNORE INTO proactive_rules(user_id, rules_yaml, updated_at) VALUES (?,?,?)`, userID, defaultYAML, now)
	return err
}
