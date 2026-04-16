package store_test

import (
	"errors"
	"testing"

	"tether/internal/store"
)

func TestCreateUser_RoleFirstUserAdmin(t *testing.T) {
	d := openTestDB(t)

	u1, err := store.CreateUser(d, "alice", "pw")
	if err != nil {
		t.Fatal(err)
	}
	if u1.Role != "admin" {
		t.Fatalf("expected first user admin, got %q", u1.Role)
	}

	u2, err := store.CreateUser(d, "bob", "pw")
	if err != nil {
		t.Fatal(err)
	}
	if u2.Role != "user" {
		t.Fatalf("expected second user user, got %q", u2.Role)
	}
}

func TestCreateUser_UsernameTaken(t *testing.T) {
	d := openTestDB(t)
	_, _ = store.CreateUser(d, "alice", "pw")
	_, err := store.CreateUser(d, "alice", "pw2")
	if !errors.Is(err, store.ErrUsernameTaken) {
		t.Fatalf("expected ErrUsernameTaken, got %v", err)
	}
}

func TestAuthenticate_SuccessAndInvalid(t *testing.T) {
	d := openTestDB(t)
	_, _ = store.CreateUser(d, "alice", "pw")

	if _, err := store.Authenticate(d, "alice", "wrong"); !errors.Is(err, store.ErrInvalidCredentials) {
		t.Fatalf("expected invalid credentials, got %v", err)
	}

	u, err := store.Authenticate(d, " alice ", "pw")
	if err != nil {
		t.Fatal(err)
	}
	if u.Username != "alice" {
		t.Fatalf("unexpected username %q", u.Username)
	}

	// last_login_at should be set (non-null)
	var lastLogin any
	if err := d.QueryRow(`SELECT last_login_at FROM users WHERE id = ?`, u.ID).Scan(&lastLogin); err != nil {
		t.Fatal(err)
	}
	if lastLogin == nil {
		t.Fatalf("expected last_login_at to be set")
	}
}
