package chatcmd

import (
	"encoding/base64"
	"strings"
	"testing"

	"tether/internal/store"
	"tether/internal/testutil"
)

func TestMemoryLifecycle(t *testing.T) {
	db := testutil.OpenTestDB(t)
	u, err := store.CreateUser(db, "alice", "pw")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	if got := MemoryList(db, u.ID, ""); got != "no memory items" {
		t.Fatalf("empty list: %q", got)
	}

	add := MemoryAdd(db, u.ID, "fact", "buy milk")
	if !strings.HasPrefix(add, "memory added (id ") {
		t.Fatalf("add: %q", add)
	}

	list := MemoryList(db, u.ID, "")
	if !strings.HasPrefix(list, "Memory:\n- ") || !strings.Contains(list, "[fact] buy milk") {
		t.Fatalf("list after add: %q", list)
	}

	// Recover the id from the listing to exercise update/delete.
	id := firstID(t, list)
	if got := MemoryUpdate(db, u.ID, id, "buy oat milk"); got != "memory updated" {
		t.Fatalf("update: %q", got)
	}
	if got := MemoryList(db, u.ID, ""); !strings.Contains(got, "buy oat milk") {
		t.Fatalf("list after update: %q", got)
	}
	if got := MemoryDelete(db, u.ID, id); got != "memory deleted" {
		t.Fatalf("delete: %q", got)
	}
	if got := MemoryList(db, u.ID, ""); got != "no memory items" {
		t.Fatalf("list after delete: %q", got)
	}
}

func TestMemoryInvalidID(t *testing.T) {
	db := testutil.OpenTestDB(t)
	u, _ := store.CreateUser(db, "alice", "pw")
	if got := MemoryUpdate(db, u.ID, "notanint", "x"); got != "invalid id" {
		t.Fatalf("update invalid: %q", got)
	}
	if got := MemoryDelete(db, u.ID, "notanint"); got != "invalid id" {
		t.Fatalf("delete invalid: %q", got)
	}
}

func TestTaskLifecycle(t *testing.T) {
	db := testutil.OpenTestDB(t)
	u, _ := store.CreateUser(db, "alice", "pw")

	if got := TaskList(db, u.ID); got != "no tasks" {
		t.Fatalf("empty: %q", got)
	}
	if got, ok := TaskAdd(db, u.ID, "ship it"); !ok || !strings.HasPrefix(got, "task added (id ") {
		t.Fatalf("add: %q ok=%v", got, ok)
	}
	list := TaskList(db, u.ID)
	if !strings.HasPrefix(list, "Tasks:\n- ") || !strings.Contains(list, ": ship it") {
		t.Fatalf("list: %q", list)
	}
	id := firstID(t, list)
	if got, ok := TaskUpdate(db, u.ID, id, "ship it twice"); !ok || got != "task updated" {
		t.Fatalf("update: %q ok=%v", got, ok)
	}
	if got, ok := TaskDone(db, u.ID, id); !ok || got != "task marked done" {
		t.Fatalf("done: %q ok=%v", got, ok)
	}
	if got, ok := TaskUpdate(db, u.ID, "notanint", "x"); ok || got != "invalid id" {
		t.Fatalf("update invalid: %q ok=%v", got, ok)
	}
	if got := TaskList(db, u.ID); got != "no tasks" {
		t.Fatalf("after done: %q", got)
	}
}

func TestSecretLifecycle(t *testing.T) {
	db := testutil.OpenTestDB(t)
	u, _ := store.CreateUser(db, "alice", "pw")
	key := base64.StdEncoding.EncodeToString(make([]byte, 32))

	s, errMsg := OpenSecretStore(db, key, 24)
	if s == nil {
		t.Fatalf("open store: %q", errMsg)
	}

	if got := SecretList(s, u.ID); got != "no secrets set" {
		t.Fatalf("empty: %q", got)
	}
	if got := SecretAdd(s, u.ID, "api", "topsecret", 24); !strings.HasPrefix(got, "secret stored as 'api'") {
		t.Fatalf("add: %q", got)
	}
	if got := SecretList(s, u.ID); !strings.Contains(got, "Secrets (labels only):") || !strings.Contains(got, "- api (expires") {
		t.Fatalf("list: %q", got)
	}
	// Plaintext must never appear in any response string.
	if strings.Contains(SecretList(s, u.ID), "topsecret") {
		t.Fatalf("secret list leaked plaintext")
	}
	if got := SecretDelete(s, u.ID, "api"); got != "deleted secret 'api'" {
		t.Fatalf("delete: %q", got)
	}
	if got := SecretClear(s, u.ID); got != "cleared all secrets" {
		t.Fatalf("clear: %q", got)
	}
}

func TestOpenSecretStoreMissingKey(t *testing.T) {
	db := testutil.OpenTestDB(t)
	s, errMsg := OpenSecretStore(db, "", 24)
	if s != nil || !strings.HasPrefix(errMsg, "secrets unavailable:") {
		t.Fatalf("expected unavailable, got store=%v msg=%q", s, errMsg)
	}
}

// firstID extracts the leading numeric id from the first "- <id>" list row.
func firstID(t *testing.T, listing string) string {
	t.Helper()
	for _, line := range strings.Split(listing, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "- ") {
			continue
		}
		rest := strings.TrimPrefix(line, "- ")
		// id is the run of digits up to the first space or ':' or '['.
		end := strings.IndexAny(rest, " :[")
		if end <= 0 {
			continue
		}
		return rest[:end]
	}
	t.Fatalf("no id row in listing: %q", listing)
	return ""
}
