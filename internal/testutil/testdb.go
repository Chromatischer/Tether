package testutil

import (
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"

	"tether/internal/db"
)

// OpenTestDB opens an in-memory SQLite DB and runs all migrations.
func OpenTestDB(t *testing.T) *sql.DB {
	t.Helper()
	d, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.Migrate(d); err != nil {
		_ = d.Close()
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d
}
