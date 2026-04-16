package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
	"time"
)

const identityTypeSignal = "signal_number"

func LinkSignalNumber(db *sql.DB, userID int64, number string) error {
	number = strings.TrimSpace(number)
	_, err := db.Exec(`INSERT OR REPLACE INTO identities(user_id, type, value, verified_at) VALUES (?, ?, ?, strftime('%Y-%m-%dT%H:%M:%fZ','now'))`, userID, identityTypeSignal, number)
	return err
}

func UnlinkSignalNumber(db *sql.DB, userID int64) error {
	_, err := db.Exec(`DELETE FROM identities WHERE user_id=? AND type=?`, userID, identityTypeSignal)
	return err
}

func GetSignalNumber(db *sql.DB, userID int64) (string, bool, error) {
	var v string
	err := db.QueryRow(`SELECT value FROM identities WHERE user_id=? AND type=?`, userID, identityTypeSignal).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return v, true, nil
}

func FindUserIDBySignalNumber(db *sql.DB, number string) (int64, bool, error) {
	var id int64
	err := db.QueryRow(`SELECT user_id FROM identities WHERE type=? AND value=?`, identityTypeSignal, strings.TrimSpace(number)).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return id, true, nil
}

func CreateSignalLinkCode(db *sql.DB, userID int64, ttl time.Duration) (string, error) {
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	code, err := randomCode(12)
	if err != nil {
		return "", err
	}
	now := time.Now().Unix()
	exp := time.Now().Add(ttl).Unix()
	_, err = db.Exec(`INSERT OR REPLACE INTO signal_link_codes(code,user_id,created_at,expires_at) VALUES (?,?,?,?)`, code, userID, now, exp)
	return code, err
}

func ConsumeSignalLinkCode(db *sql.DB, code string) (userID int64, ok bool, err error) {
	code = strings.TrimSpace(code)
	var uid int64
	var exp int64
	err = db.QueryRow(`SELECT user_id, expires_at FROM signal_link_codes WHERE code=?`, code).Scan(&uid, &exp)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	if exp <= time.Now().Unix() {
		_, _ = db.Exec(`DELETE FROM signal_link_codes WHERE code=?`, code)
		return 0, false, nil
	}
	_, _ = db.Exec(`DELETE FROM signal_link_codes WHERE code=?`, code)
	return uid, true, nil
}

func PruneSignalLinkCodes(db *sql.DB) {
	_, _ = db.Exec(`DELETE FROM signal_link_codes WHERE expires_at <= ?`, time.Now().Unix())
}

func CountLinkedSignalNumbers(db *sql.DB) (int, error) {
	var c int
	err := db.QueryRow(`SELECT COUNT(1) FROM identities WHERE type=?`, identityTypeSignal).Scan(&c)
	return c, err
}

func randomCode(nbytes int) (string, error) {
	b := make([]byte, nbytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
