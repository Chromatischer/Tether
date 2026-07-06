package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
	"time"
)

const identityTypeDiscord = "discord_user_id"

// userSettingDiscordVerbosity controls how much of the agent's streaming output
// is surfaced in Discord DMs (tool calls, reasoning, final message).
const userSettingDiscordVerbosity = "discord_verbosity"

// Discord verbosity levels.
//
//   - DiscordVerbosityFull: stream tool calls + reasoning + the final message.
//   - DiscordVerbosityNoThinking: stream tool calls + the final message (no reasoning).
//   - DiscordVerbosityMessageOnly: only the final message (no tool calls, no reasoning).
const (
	DiscordVerbosityFull        = "full"
	DiscordVerbosityNoThinking  = "no_thinking"
	DiscordVerbosityMessageOnly = "message_only"
)

// DiscordVerbosityDefault is used when a user has not chosen a level.
const DiscordVerbosityDefault = DiscordVerbosityFull

// ValidDiscordVerbosity reports whether v is a recognized verbosity level.
func ValidDiscordVerbosity(v string) bool {
	switch v {
	case DiscordVerbosityFull, DiscordVerbosityNoThinking, DiscordVerbosityMessageOnly:
		return true
	default:
		return false
	}
}

// GetDiscordVerbosity returns the user's configured Discord verbosity level,
// falling back to DiscordVerbosityDefault when unset or invalid.
func GetDiscordVerbosity(db *sql.DB, userID int64) string {
	v, ok, err := GetUserSetting(db, userID, userSettingDiscordVerbosity)
	if err != nil || !ok || !ValidDiscordVerbosity(v) {
		return DiscordVerbosityDefault
	}
	return v
}

// SetDiscordVerbosity stores the user's preferred Discord verbosity level.
func SetDiscordVerbosity(db *sql.DB, userID int64, v string) error {
	if !ValidDiscordVerbosity(v) {
		return errors.New("invalid discord verbosity level")
	}
	return SetUserSetting(db, userID, userSettingDiscordVerbosity, v)
}

var ErrDiscordAlreadyLinkedForUser = errors.New("discord already linked for user")

func LinkDiscordUserID(db *sql.DB, userID int64, discordUserID string) error {
	discordUserID = strings.TrimSpace(discordUserID)
	if discordUserID == "" {
		return errors.New("discord user id is empty")
	}

	// Refuse if this user already has a linked Discord account.
	if _, ok, err := GetDiscordUserID(db, userID); err != nil {
		return err
	} else if ok {
		return ErrDiscordAlreadyLinkedForUser
	}

	_, err := db.Exec(
		`INSERT INTO identities(user_id, type, value, verified_at) VALUES (?, ?, ?, strftime('%Y-%m-%dT%H:%M:%fZ','now'))`,
		userID,
		identityTypeDiscord,
		discordUserID,
	)
	return err
}

func UnlinkDiscordUserID(db *sql.DB, userID int64) error {
	_, err := db.Exec(`DELETE FROM identities WHERE user_id=? AND type=?`, userID, identityTypeDiscord)
	return err
}

func GetDiscordUserID(db *sql.DB, userID int64) (string, bool, error) {
	var v string
	err := db.QueryRow(`SELECT value FROM identities WHERE user_id=? AND type=?`, userID, identityTypeDiscord).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return v, true, nil
}

func FindUserIDByDiscordUserID(db *sql.DB, discordUserID string) (int64, bool, error) {
	var id int64
	err := db.QueryRow(`SELECT user_id FROM identities WHERE type=? AND value=?`, identityTypeDiscord, strings.TrimSpace(discordUserID)).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return id, true, nil
}

func CountLinkedDiscordUsers(db *sql.DB) (int, error) {
	var c int
	err := db.QueryRow(`SELECT COUNT(1) FROM identities WHERE type=?`, identityTypeDiscord).Scan(&c)
	return c, err
}

func CreateDiscordLinkCode(db *sql.DB, discordUserID string, ttl time.Duration) (string, error) {
	discordUserID = strings.TrimSpace(discordUserID)
	if discordUserID == "" {
		return "", errors.New("discord user id is empty")
	}
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	code, err := randomDiscordCode(12)
	if err != nil {
		return "", err
	}
	now := time.Now().Unix()
	exp := time.Now().Add(ttl).Unix()
	// Unique index on discord_user_id ensures we keep at most one active code per Discord user.
	_, err = db.Exec(`INSERT OR REPLACE INTO discord_link_codes(code, discord_user_id, created_at, expires_at) VALUES (?,?,?,?)`, code, discordUserID, now, exp)
	return code, err
}

func ConsumeDiscordLinkCode(db *sql.DB, code string) (discordUserID string, ok bool, err error) {
	code = strings.TrimSpace(code)
	var duid string
	var exp int64
	err = db.QueryRow(`SELECT discord_user_id, expires_at FROM discord_link_codes WHERE code=?`, code).Scan(&duid, &exp)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if exp <= time.Now().Unix() {
		_, _ = db.Exec(`DELETE FROM discord_link_codes WHERE code=?`, code)
		return "", false, nil
	}
	_, _ = db.Exec(`DELETE FROM discord_link_codes WHERE code=?`, code)
	return duid, true, nil
}

func PruneDiscordLinkCodes(db *sql.DB) {
	_, _ = db.Exec(`DELETE FROM discord_link_codes WHERE expires_at <= ?`, time.Now().Unix())
}

func randomDiscordCode(nbytes int) (string, error) {
	b := make([]byte, nbytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
