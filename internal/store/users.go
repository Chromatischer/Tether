package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrUsernameTaken      = errors.New("username already taken")
)

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
