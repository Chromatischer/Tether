package cache

import (
	"database/sql"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"tether/internal/db"
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

func TestLLMCache_PutGet(t *testing.T) {
	d := openTestDB(t)
	c := NewLLMCache(d, time.Hour)
	key := KeyFromBytes([]byte("hello"))
	if err := c.Put(key, "resp"); err != nil {
		t.Fatal(err)
	}
	got, ok, err := c.Get(key)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || got != "resp" {
		t.Fatalf("expected hit resp, got %q ok=%v", got, ok)
	}
}

func TestLLMCache_GetExpiredDeletes(t *testing.T) {
	d := openTestDB(t)
	c := NewLLMCache(d, time.Hour)
	key := KeyFromBytes([]byte("hello"))
	if err := c.Put(key, "resp"); err != nil {
		t.Fatal(err)
	}
	// Force expiry.
	if _, err := d.Exec(`UPDATE llm_cache SET expires_at = ? WHERE cache_key = ?`, time.Now().Add(-time.Minute).Unix(), key); err != nil {
		t.Fatal(err)
	}
	_, ok, err := c.Get(key)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatalf("expected miss")
	}
	var n int
	_ = d.QueryRow(`SELECT COUNT(1) FROM llm_cache WHERE cache_key = ?`, key).Scan(&n)
	if n != 0 {
		t.Fatalf("expected expired entry deleted")
	}
}

func TestLLMCache_PruneExpired(t *testing.T) {
	d := openTestDB(t)
	c := NewLLMCache(d, time.Hour)
	key1 := KeyFromBytes([]byte("k1"))
	key2 := KeyFromBytes([]byte("k2"))
	_ = c.Put(key1, "r1")
	_ = c.Put(key2, "r2")
	if _, err := d.Exec(`UPDATE llm_cache SET expires_at = ? WHERE cache_key = ?`, time.Now().Add(-time.Minute).Unix(), key1); err != nil {
		t.Fatal(err)
	}
	if err := c.PruneExpired(); err != nil {
		t.Fatal(err)
	}
	_, ok1, _ := c.Get(key1)
	_, ok2, _ := c.Get(key2)
	if ok1 {
		t.Fatalf("expected key1 pruned")
	}
	if !ok2 {
		t.Fatalf("expected key2 to remain")
	}
}
