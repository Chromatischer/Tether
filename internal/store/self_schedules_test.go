package store_test

import (
	"testing"
	"time"

	"tether/internal/store"
)

func TestSelfSchedules_CreateListClaimDone(t *testing.T) {
	d := openTestDB(t)
	u, err := store.CreateUser(d, "alice", "pw")
	if err != nil {
		t.Fatal(err)
	}
	c, err := store.GetOrCreateDefaultConversation(d, u.ID)
	if err != nil {
		t.Fatal(err)
	}

	_ = store.AddMessage(d, c.ID, "user", "m1")
	_ = store.AddMessage(d, c.ID, "assistant", "m2")
	_ = store.AddMessage(d, c.ID, "user", "m3")

	lastID, ok, err := store.LatestMessageID(d, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || lastID <= 0 {
		t.Fatalf("expected latest message id")
	}

	msgs, err := store.ListRecentMessagesBeforeID(d, c.ID, lastID-1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2 msgs, got %d", len(msgs))
	}
	if msgs[0].Content != "m1" || msgs[1].Content != "m2" {
		t.Fatalf("unexpected messages: %+v", msgs)
	}

	runAt := time.Now().UTC().Add(-1 * time.Second)
	id, err := store.CreateSelfSchedule(d, u.ID, c.ID, lastID, "do a follow-up", runAt, `["tool.search","read"]`)
	if err != nil {
		t.Fatal(err)
	}

	due, err := store.ListDueSelfSchedules(d, u.ID, time.Now().UTC(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 1 || due[0].ID != id {
		t.Fatalf("expected 1 due schedule")
	}

	claimed, err := store.TryClaimSelfSchedule(d, id)
	if err != nil {
		t.Fatal(err)
	}
	if !claimed {
		t.Fatalf("expected claimed=true")
	}
	claimed2, _ := store.TryClaimSelfSchedule(d, id)
	if claimed2 {
		t.Fatalf("expected claimed=false on second claim")
	}

	if err := store.MarkSelfScheduleDone(d, id); err != nil {
		t.Fatal(err)
	}

	due2, err := store.ListDueSelfSchedules(d, u.ID, time.Now().UTC(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(due2) != 0 {
		t.Fatalf("expected 0 due after done")
	}
}

func TestSelfSchedules_Cancel(t *testing.T) {
	d := openTestDB(t)
	u, _ := store.CreateUser(d, "alice", "pw")
	c, _ := store.GetOrCreateDefaultConversation(d, u.ID)

	id, err := store.CreateSelfSchedule(d, u.ID, c.ID, 0, "later", time.Now().UTC().Add(time.Hour), `[]`)
	if err != nil {
		t.Fatal(err)
	}
	ok, err := store.CancelSelfSchedule(d, u.ID, c.ID, id)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatalf("expected canceled")
	}
	ok2, _ := store.CancelSelfSchedule(d, u.ID, c.ID, id)
	if ok2 {
		t.Fatalf("expected cancel to be idempotent false")
	}
}
