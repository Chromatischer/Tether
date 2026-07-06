package proactive

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"

	"tether/internal/db"
	"tether/internal/store"
)

type fakeNotifier struct {
	reachable bool
	got       []string
}

func (f *fakeNotifier) DeliverMessage(ctx context.Context, userID, conversationID int64, text string) (bool, error) {
	if !f.reachable {
		return false, nil
	}
	f.got = append(f.got, text)
	return true, nil
}

func newDeliverTestEngine(t *testing.T) (*Engine, *sql.DB, int64, int64) {
	t.Helper()
	d, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(d); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	u, err := store.CreateUser(d, "alice", "pw")
	if err != nil {
		t.Fatal(err)
	}
	conv, err := store.CreateConversation(d, u.ID, "t")
	if err != nil {
		t.Fatal(err)
	}
	return NewEngine(d, nil, nil, nil, t.TempDir()), d, u.ID, conv.ID
}

func TestDeliverProactiveMessage_ExternalChannelPersistsAssistant(t *testing.T) {
	eng, d, uid, conv := newDeliverTestEngine(t)
	fn := &fakeNotifier{reachable: true}
	eng.RegisterNotifier(fn)

	eng.deliverProactiveMessage(context.Background(), uid, conv, "self_schedule", "hello there")

	if len(fn.got) != 1 || fn.got[0] != "hello there" {
		t.Fatalf("expected external delivery, got %v", fn.got)
	}
	msgs, _ := store.ListRecentMessages(d, conv, 10)
	found := false
	for _, m := range msgs {
		if m.Role == "assistant" && m.Content == "hello there" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a normal assistant message persisted, got %+v", msgs)
	}
	if nots, _ := store.ListUndeliveredNotifications(d, uid, 10); len(nots) != 0 {
		t.Fatalf("expected no in-app notification when delivered externally, got %d", len(nots))
	}
}

func TestDeliverProactiveMessage_FallsBackToNotification(t *testing.T) {
	eng, d, uid, conv := newDeliverTestEngine(t)
	eng.RegisterNotifier(&fakeNotifier{reachable: false})

	eng.deliverProactiveMessage(context.Background(), uid, conv, "self_schedule", "fallback msg")

	nots, _ := store.ListUndeliveredNotifications(d, uid, 10)
	if len(nots) != 1 || nots[0].Content != "fallback msg" {
		t.Fatalf("expected fallback in-app notification, got %v", nots)
	}
	msgs, _ := store.ListRecentMessages(d, conv, 10)
	for _, m := range msgs {
		if m.Role == "assistant" {
			t.Fatalf("did not expect an assistant message on the fallback path")
		}
	}
}
