package store_test

import (
	"database/sql"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"tether/internal/db"
	"tether/internal/store"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	d, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(d); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func TestUpdateMemoryItem(t *testing.T) {
	d := openTestDB(t)
	id, err := store.AddMemoryItem(d, 1, "fact", "original")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateMemoryItem(d, 1, id, "updated"); err != nil {
		t.Fatal(err)
	}
	items, err := store.ListMemoryItems(d, 1, "fact", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Content != "updated" {
		t.Fatalf("expected updated content, got %v", items)
	}
}

func TestSetMemoryPin(t *testing.T) {
	d := openTestDB(t)
	id, _ := store.AddMemoryItem(d, 1, "fact", "pinnable")
	if err := store.SetMemoryPin(d, 1, id, true); err != nil {
		t.Fatal(err)
	}
	items, _ := store.ListMemoryItems(d, 1, "fact", 10)
	if len(items) == 0 || !items[0].Pinned {
		t.Fatal("expected pinned=true")
	}
}

func TestSetMemoryExpiry(t *testing.T) {
	d := openTestDB(t)
	id, _ := store.AddMemoryItem(d, 1, "fact", "expirable")
	past := time.Now().Add(-time.Hour)
	if err := store.SetMemoryExpiry(d, 1, id, &past); err != nil {
		t.Fatal(err)
	}
	// Expired item should not appear
	items, _ := store.ListMemoryItems(d, 1, "fact", 10)
	if len(items) != 0 {
		t.Fatalf("expected expired item to be filtered out, got %d items", len(items))
	}
}

func TestTouchMemoryItems(t *testing.T) {
	d := openTestDB(t)
	id, _ := store.AddMemoryItem(d, 1, "fact", "touchable")
	if err := store.TouchMemoryItems(d, []int64{id}); err != nil {
		t.Fatal(err)
	}
	items, _ := store.ListMemoryItems(d, 1, "fact", 10)
	if len(items) == 0 || items[0].LastUsedAt == nil {
		t.Fatal("expected last_used_at to be set")
	}
}

func TestListMemoryItemsPinnedFirst(t *testing.T) {
	d := openTestDB(t)
	id1, _ := store.AddMemoryItem(d, 1, "fact", "regular")
	id2, _ := store.AddMemoryItem(d, 1, "fact", "pinned")
	_ = store.SetMemoryPin(d, 1, id2, true)
	items, _ := store.ListMemoryItems(d, 1, "fact", 10)
	if len(items) < 2 || items[0].ID != id2 {
		t.Fatalf("expected pinned item (id=%d) first, got id=%d (id1=%d id2=%d)", id2, items[0].ID, id1, id2)
	}
}
