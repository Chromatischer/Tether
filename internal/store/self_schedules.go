package store

import (
	"database/sql"
	"strings"
	"time"
)

type SelfSchedule struct {
	ID              int64
	UserID          int64
	ConversationID  int64
	AnchorMessageID int64
	Prompt          string
	RunAt           time.Time
	CreatedAt       time.Time
	ClaimedAt       time.Time
	DoneAt          time.Time
	CanceledAt      time.Time
	Error           string

	ActiveToolsJSON string
}

func CreateSelfSchedule(db *sql.DB, userID, conversationID, anchorMessageID int64, prompt string, runAt time.Time, activeToolsJSON string) (int64, error) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		prompt = "(no prompt)"
	}
	now := time.Now().UTC().Unix()
	res, err := db.Exec(
		`INSERT INTO self_schedules(user_id, conversation_id, anchor_message_id, prompt, run_at, created_at, active_tools_json) VALUES (?,?,?,?,?,?,?)`,
		userID, conversationID, anchorMessageID, prompt, runAt.UTC().Unix(), now, strings.TrimSpace(activeToolsJSON),
	)
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	return id, nil
}

func ListDueSelfSchedules(db *sql.DB, userID int64, now time.Time, limit int) ([]SelfSchedule, error) {
	if limit <= 0 {
		limit = 20
	}
	staleBefore := now.UTC().Add(-10 * time.Minute).Unix()
	rows, err := db.Query(
		`SELECT id, user_id, conversation_id, anchor_message_id, prompt, run_at, created_at, COALESCE(active_tools_json,'[]')
		 FROM self_schedules
		 WHERE user_id=? AND run_at <= ? AND (claimed_at IS NULL OR claimed_at < ?) AND done_at IS NULL AND canceled_at IS NULL
		 ORDER BY run_at ASC, id ASC
		 LIMIT ?`,
		userID, now.UTC().Unix(), staleBefore, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SelfSchedule{}
	for rows.Next() {
		var ss SelfSchedule
		var runAt, createdAt int64
		if err := rows.Scan(&ss.ID, &ss.UserID, &ss.ConversationID, &ss.AnchorMessageID, &ss.Prompt, &runAt, &createdAt, &ss.ActiveToolsJSON); err != nil {
			return nil, err
		}
		ss.RunAt = time.Unix(runAt, 0).UTC()
		ss.CreatedAt = time.Unix(createdAt, 0).UTC()
		out = append(out, ss)
	}
	return out, rows.Err()
}

func ListPendingSelfSchedules(db *sql.DB, userID, conversationID int64, limit int) ([]SelfSchedule, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := db.Query(
		`SELECT id, user_id, conversation_id, anchor_message_id, prompt, run_at, created_at, COALESCE(claimed_at,0), COALESCE(done_at,0), COALESCE(canceled_at,0), COALESCE(error,''), COALESCE(active_tools_json,'[]')
		 FROM self_schedules
		 WHERE user_id=? AND conversation_id=? AND done_at IS NULL AND canceled_at IS NULL
		 ORDER BY run_at ASC, id ASC
		 LIMIT ?`,
		userID, conversationID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SelfSchedule{}
	for rows.Next() {
		var ss SelfSchedule
		var runAt, createdAt, claimedAt, doneAt, canceledAt int64
		if err := rows.Scan(&ss.ID, &ss.UserID, &ss.ConversationID, &ss.AnchorMessageID, &ss.Prompt, &runAt, &createdAt, &claimedAt, &doneAt, &canceledAt, &ss.Error, &ss.ActiveToolsJSON); err != nil {
			return nil, err
		}
		ss.RunAt = time.Unix(runAt, 0).UTC()
		ss.CreatedAt = time.Unix(createdAt, 0).UTC()
		if claimedAt > 0 {
			ss.ClaimedAt = time.Unix(claimedAt, 0).UTC()
		}
		if doneAt > 0 {
			ss.DoneAt = time.Unix(doneAt, 0).UTC()
		}
		if canceledAt > 0 {
			ss.CanceledAt = time.Unix(canceledAt, 0).UTC()
		}
		out = append(out, ss)
	}
	return out, rows.Err()
}

func TryClaimSelfSchedule(db *sql.DB, id int64) (bool, error) {
	now := time.Now().UTC()
	staleBefore := now.Add(-10 * time.Minute).Unix()
	res, err := db.Exec(`UPDATE self_schedules SET claimed_at=? WHERE id=? AND (claimed_at IS NULL OR claimed_at < ?) AND done_at IS NULL AND canceled_at IS NULL`, now.Unix(), id, staleBefore)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

func MarkSelfScheduleDone(db *sql.DB, id int64) error {
	now := time.Now().UTC().Unix()
	_, err := db.Exec(`UPDATE self_schedules SET done_at=? WHERE id=?`, now, id)
	return err
}

func MarkSelfScheduleError(db *sql.DB, id int64, msg string) error {
	now := time.Now().UTC().Unix()
	msg = strings.TrimSpace(msg)
	if len(msg) > 500 {
		msg = msg[:500] + "…"
	}
	_, err := db.Exec(`UPDATE self_schedules SET done_at=?, error=? WHERE id=?`, now, msg, id)
	return err
}

func CancelSelfSchedule(db *sql.DB, userID, conversationID, id int64) (bool, error) {
	now := time.Now().UTC().Unix()
	res, err := db.Exec(
		`UPDATE self_schedules SET canceled_at=?
		 WHERE id=? AND user_id=? AND conversation_id=? AND done_at IS NULL AND canceled_at IS NULL`,
		now, id, userID, conversationID,
	)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}
