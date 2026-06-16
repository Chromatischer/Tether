package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrUsernameTaken      = errors.New("username already taken")
)

// RootUsername is the implicit account used by local terminal mode, which
// runs without portal authentication.
const RootUsername = "root"

// EnsureRootUser returns the local "root" account, creating it (as admin) if
// it does not exist. The password is randomized and unused: local terminal
// mode bypasses the login flow entirely.
func EnsureRootUser(db *sql.DB) (*User, error) {
	var u User
	err := db.QueryRow(`SELECT id, username, role FROM users WHERE username = ?`, RootUsername).
		Scan(&u.ID, &u.Username, &u.Role)
	if err == nil {
		return &u, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}

	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(hex.EncodeToString(secret)), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	res, err := db.Exec(`INSERT INTO users(username, pass_hash, role) VALUES (?, ?, 'admin')`, RootUsername, string(hash))
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return &User{ID: id, Username: RootUsername, Role: "admin"}, nil
}

func CreateUser(db *sql.DB, username, password string) (*User, error) {
	username = strings.TrimSpace(username)
	if username == "" {
		return nil, fmt.Errorf("username required")
	}
	if password == "" {
		return nil, fmt.Errorf("password required")
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}

	// First user becomes admin (local/private deployment convenience).
	role := "user"
	var c int
	if err := db.QueryRow(`SELECT COUNT(1) FROM users`).Scan(&c); err == nil && c == 0 {
		role = "admin"
	}

	res, err := db.Exec(`INSERT INTO users(username, pass_hash, role) VALUES (?, ?, ?)`, username, string(hash), role)
	if err != nil {
		// SQLite unique constraint error text isn't stable; keep it simple.
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return nil, ErrUsernameTaken
		}
		return nil, err
	}
	id, _ := res.LastInsertId()
	return &User{ID: id, Username: username, Role: role}, nil
}

func Authenticate(db *sql.DB, username, password string) (*User, error) {
	username = strings.TrimSpace(username)
	var u User
	var passHash string
	if err := db.QueryRow(`SELECT id, username, pass_hash, role FROM users WHERE username = ?`, username).
		Scan(&u.ID, &u.Username, &passHash, &u.Role); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrInvalidCredentials
		}
		return nil, err
	}
	if bcrypt.CompareHashAndPassword([]byte(passHash), []byte(password)) != nil {
		return nil, ErrInvalidCredentials
	}
	_, _ = db.Exec(`UPDATE users SET last_login_at = (strftime('%Y-%m-%dT%H:%M:%fZ','now')) WHERE id = ?`, u.ID)
	return &u, nil
}

func GetUserByID(db *sql.DB, userID int64) (*User, bool, error) {
	var u User
	if err := db.QueryRow(`SELECT id, username, role FROM users WHERE id = ?`, userID).
		Scan(&u.ID, &u.Username, &u.Role); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return &u, true, nil
}
