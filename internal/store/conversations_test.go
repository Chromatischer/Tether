package store_test

import (
	"testing"

	"tether/internal/store"
)

func TestGetOrCreateDefaultConversation(t *testing.T) {
	d := openTestDB(t)
	// need a user because of FK
	u, err := store.CreateUser(d, "alice", "pw")
	if err != nil {
		t.Fatal(err)
	}

	c1, err := store.GetOrCreateDefaultConversation(d, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	c2, err := store.GetOrCreateDefaultConversation(d, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if c1.ID != c2.ID {
		t.Fatalf("expected same default conversation")
	}
}

func TestAddAndListRecentMessages_OrderAndLimit(t *testing.T) {
	d := openTestDB(t)
	u, _ := store.CreateUser(d, "alice", "pw")
	c, _ := store.GetOrCreateDefaultConversation(d, u.ID)

	_ = store.AddMessage(d, c.ID, "user", "m1")
	_ = store.AddMessage(d, c.ID, "assistant", "m2")
	_ = store.AddMessage(d, c.ID, "user", "m3")

	msgs, err := store.ListRecentMessages(d, c.ID, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2, got %d", len(msgs))
	}
	// Should be chronological for the last 2 messages: m2, m3
	if msgs[0].Content != "m2" || msgs[1].Content != "m3" {
		t.Fatalf("unexpected order: %+v", msgs)
	}
}
