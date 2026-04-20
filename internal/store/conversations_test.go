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

func TestGetOrCreateActiveConversationUsesPersistedSelection(t *testing.T) {
	d := openTestDB(t)
	u, err := store.CreateUser(d, "alice", "pw")
	if err != nil {
		t.Fatal(err)
	}

	c1, err := store.GetOrCreateDefaultConversation(d, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	c2, err := store.CreateConversation(d, u.ID, "new")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetActiveConversation(d, u.ID, c2.ID); err != nil {
		t.Fatal(err)
	}

	got, err := store.GetOrCreateActiveConversation(d, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != c2.ID || got.ID == c1.ID {
		t.Fatalf("expected active conversation %d, got %d", c2.ID, got.ID)
	}
}

func TestResumeCodeRoundTrip(t *testing.T) {
	code := store.EncodeResumeCode(12345)
	got, err := store.DecodeResumeCode(code)
	if err != nil {
		t.Fatal(err)
	}
	if got != 12345 {
		t.Fatalf("expected 12345, got %d", got)
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

func TestAddMessageMarksAssistantNotices(t *testing.T) {
	d := openTestDB(t)
	u, _ := store.CreateUser(d, "alice", "pw")
	c, _ := store.GetOrCreateDefaultConversation(d, u.ID)

	_ = store.AddMessage(d, c.ID, "assistant", "usage: /resume <code>")
	_ = store.AddMessage(d, c.ID, "assistant", "Here is the actual answer.")

	msgs, err := store.ListRecentMessages(d, c.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	if !msgs[0].IsNotice {
		t.Fatalf("expected usage message to be marked as notice, got %+v", msgs[0])
	}
	if msgs[1].IsNotice {
		t.Fatalf("expected normal assistant reply to stay non-notice, got %+v", msgs[1])
	}
}
