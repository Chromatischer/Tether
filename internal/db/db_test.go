package db

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOpen_SetsPragmas(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "nested", "tether.sqlite")

	d, err := Open(p)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer d.Close()

	// journal_mode returns "wal" in lowercase for SQLite.
	var journalMode string
	if err := d.QueryRow(`PRAGMA journal_mode;`).Scan(&journalMode); err != nil {
		t.Fatalf("query journal_mode: %v", err)
	}
	if journalMode != "wal" {
		t.Fatalf("expected journal_mode=wal, got %q", journalMode)
	}

	var fk int
	if err := d.QueryRow(`PRAGMA foreign_keys;`).Scan(&fk); err != nil {
		t.Fatalf("query foreign_keys: %v", err)
	}
	if fk != 1 {
		t.Fatalf("expected foreign_keys=1, got %d", fk)
	}

	var busy int
	if err := d.QueryRow(`PRAGMA busy_timeout;`).Scan(&busy); err != nil {
		t.Fatalf("query busy_timeout: %v", err)
	}
	if busy != 5000 {
		t.Fatalf("expected busy_timeout=5000, got %d", busy)
	}

	if _, err := os.Stat(filepath.Dir(p)); err != nil {
		t.Fatalf("expected parent dir to exist: %v", err)
	}
}

func TestMigrate_IdempotentAndAppliesAll(t *testing.T) {
	dir := t.TempDir()
	d, err := Open(filepath.Join(dir, "t.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	migs, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}

	if err := Migrate(d); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if err := Migrate(d); err != nil {
		t.Fatalf("Migrate second: %v", err)
	}

	var c int
	if err := d.QueryRow(`SELECT COUNT(1) FROM schema_migrations`).Scan(&c); err != nil {
		t.Fatal(err)
	}
	if c != len(migs) {
		t.Fatalf("expected %d applied migrations, got %d", len(migs), c)
	}
}

func TestLoadMigrations_SortedByVersion(t *testing.T) {
	migs, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if len(migs) < 2 {
		t.Fatalf("expected at least 2 migrations")
	}
	for i := 1; i < len(migs); i++ {
		if migs[i].Version < migs[i-1].Version {
			t.Fatalf("migrations not sorted: %d before %d", migs[i-1].Version, migs[i].Version)
		}
	}
}
