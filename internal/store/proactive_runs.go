package store

import (
	"database/sql"
	"time"
)

// TryStartProactiveRun records a proactive run for (user, agent, trigger, day_key).
// It returns started=false if a run with the same unique key already exists.
func TryStartProactiveRun(db *sql.DB, userID int64, agentID string, trigger string, dayKey string) (started bool, err error) {
	now := time.Now().Unix()
	res, err := db.Exec(`INSERT OR IGNORE INTO proactive_runs(user_id, agent_id, trigger, day_key, created_at) VALUES (?,?,?,?,?)`, userID, agentID, trigger, dayKey, now)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

func CountProactiveRunsForDay(db *sql.DB, userID int64, agentID string, dayKey string) (int, error) {
	var c int
	err := db.QueryRow(`SELECT COUNT(1) FROM proactive_runs WHERE user_id=? AND agent_id=? AND day_key=?`, userID, agentID, dayKey).Scan(&c)
	return c, err
}

func LatestProactiveRunTime(db *sql.DB, userID int64, agentID string) (time.Time, bool, error) {
	var created int64
	err := db.QueryRow(`SELECT created_at FROM proactive_runs WHERE user_id=? AND agent_id=? ORDER BY created_at DESC LIMIT 1`, userID, agentID).Scan(&created)
	if err == sql.ErrNoRows {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, err
	}
	return time.Unix(created, 0), true, nil
}
