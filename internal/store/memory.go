package store

import (
	"database/sql"
	"errors"
	"strings"
	"time"
)

type MemoryItem struct {
	ID         int64
	UserID     int64
	Kind       string
	Content    string
	Pinned     bool
	ExpiresAt  *time.Time
	LastUsedAt *time.Time
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

func AddMemoryItem(db *sql.DB, userID int64, kind, content string) (int64, error) {
	kind = strings.TrimSpace(kind)
	if err := validateMemoryKind(kind); err != nil {
		return 0, err
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return 0, errors.New("content required")
	}
	now := time.Now().Unix()
	res, err := db.Exec(
		`INSERT INTO memory_items(user_id, kind, content, created_at, updated_at) VALUES (?,?,?,?,?)`,
		userID, kind, content, now, now,
	)
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	return id, nil
}

func UpdateMemoryItem(db *sql.DB, userID, id int64, content string) error {
	content = strings.TrimSpace(content)
	if content == "" {
		return errors.New("content required")
	}
	now := time.Now().Unix()
	_, err := db.Exec(
		`UPDATE memory_items SET content=?, updated_at=? WHERE user_id=? AND id=?`,
		content, now, userID, id,
	)
	return err
}

func SetMemoryPin(db *sql.DB, userID, id int64, pinned bool) error {
	v := 0
	if pinned {
		v = 1
	}
	_, err := db.Exec(
		`UPDATE memory_items SET pinned=? WHERE user_id=? AND id=?`,
		v, userID, id,
	)
	return err
}

func SetMemoryExpiry(db *sql.DB, userID, id int64, t *time.Time) error {
	if t == nil {
		_, err := db.Exec(
			`UPDATE memory_items SET expires_at=NULL WHERE user_id=? AND id=?`,
			userID, id,
		)
		return err
	}
	_, err := db.Exec(
		`UPDATE memory_items SET expires_at=? WHERE user_id=? AND id=?`,
		t.Unix(), userID, id,
	)
	return err
}

// TouchMemoryItems sets last_used_at=now for each given ID.
// IDs that don't exist are silently skipped.
func TouchMemoryItems(db *sql.DB, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	now := time.Now().Unix()
	for _, id := range ids {
		if _, err := db.Exec(
			`UPDATE memory_items SET last_used_at=? WHERE id=?`,
			now, id,
		); err != nil {
			return err
		}
	}
	return nil
}

func validateMemoryKind(kind string) error {
	switch strings.TrimSpace(kind) {
	case "fact", "pref", "task":
		return nil
	default:
		return errors.New("invalid kind (expected fact|pref|task)")
	}
}

// ListMemoryItems returns items for a user, filtered by kind (empty = all),
// excluding expired items, sorted pinned-first then newest-first.
func ListMemoryItems(db *sql.DB, userID int64, kind string, limit int) ([]MemoryItem, error) {
	if limit <= 0 {
		limit = 100
	}
	now := time.Now().Unix()
	rows, err := db.Query(`
		SELECT id, user_id, kind, content, pinned,
		       expires_at, last_used_at, created_at, updated_at
		FROM memory_items
		WHERE user_id=?
		  AND (?='' OR kind=?)
		  AND (expires_at IS NULL OR expires_at > ?)
		ORDER BY pinned DESC, id DESC
		LIMIT ?`,
		userID, kind, kind, now, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MemoryItem{}
	for rows.Next() {
		var it MemoryItem
		var pinned int
		var expiresAt, lastUsedAt sql.NullInt64
		var cAt, uAt int64
		if err := rows.Scan(
			&it.ID, &it.UserID, &it.Kind, &it.Content, &pinned,
			&expiresAt, &lastUsedAt, &cAt, &uAt,
		); err != nil {
			return nil, err
		}
		it.Pinned = pinned != 0
		if expiresAt.Valid {
			t := time.Unix(expiresAt.Int64, 0)
			it.ExpiresAt = &t
		}
		if lastUsedAt.Valid {
			t := time.Unix(lastUsedAt.Int64, 0)
			it.LastUsedAt = &t
		}
		it.CreatedAt = time.Unix(cAt, 0)
		it.UpdatedAt = time.Unix(uAt, 0)
		out = append(out, it)
	}
	return out, rows.Err()
}

func DeleteMemoryItem(db *sql.DB, userID, id int64) error {
	_, err := db.Exec(`DELETE FROM memory_items WHERE user_id=? AND id=?`, userID, id)
	return err
}
