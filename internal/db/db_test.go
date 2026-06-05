package db

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
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

	var count int
	if err := d.QueryRow(`SELECT COUNT(1) FROM schema_migrations`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != len(migs) {
		t.Fatalf("expected %d applied migrations, got %d", len(migs), count)
	}

	var maxVersion int
	if err := d.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&maxVersion); err != nil {
		t.Fatal(err)
	}
	if maxVersion != migs[len(migs)-1].Version {
		t.Fatalf("expected latest migration version %d, got %d", migs[len(migs)-1].Version, maxVersion)
	}

	var notNull int
	if err := d.QueryRow(`SELECT COUNT(1) FROM pragma_table_info('messages') WHERE name='is_notice' AND "notnull" = 1`).Scan(&notNull); err != nil {
		t.Fatal(err)
	}
	if notNull != 1 {
		t.Fatalf("expected messages.is_notice column to exist and be NOT NULL, got count=%d", notNull)
	}

	var changelogColumn int
	if err := d.QueryRow(`SELECT COUNT(1) FROM pragma_table_info('users') WHERE name='last_seen_changelog_version'`).Scan(&changelogColumn); err != nil {
		t.Fatal(err)
	}
	if changelogColumn != 1 {
		t.Fatalf("expected users.last_seen_changelog_version column to exist, got count=%d", changelogColumn)
	}
}

func TestMigrate_AllowsExistingDBAtBaseline(t *testing.T) {
	dir := t.TempDir()
	d, err := Open(filepath.Join(dir, "t.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	if _, err := d.Exec(bootstrapMigrationsTable); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`CREATE TABLE users (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		username TEXT NOT NULL UNIQUE,
		pass_hash TEXT NOT NULL,
		role TEXT NOT NULL DEFAULT 'user',
		created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
		last_login_at TEXT
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`INSERT INTO schema_migrations(version) VALUES (?)`, currentSchemaBaseline.Version); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(d); err != nil {
		t.Fatalf("Migrate existing baseline DB: %v", err)
	}
}

func TestMigrate_RejectsExistingDBBeforeBaseline(t *testing.T) {
	d, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	if _, err := d.Exec(bootstrapMigrationsTable); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`INSERT INTO schema_migrations(version) VALUES (?)`, currentSchemaBaseline.Version-1); err != nil {
		t.Fatal(err)
	}
	err = Migrate(d)
	if err == nil {
		t.Fatal("expected baseline error, got nil")
	}
	if !strings.Contains(err.Error(), "older than the "+currentSchemaBaseline.Release+" baseline") {
		t.Fatalf("expected baseline error, got %v", err)
	}
}

func TestLoadMigrations_BaselineVersion(t *testing.T) {
	migs, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if len(migs) == 0 {
		t.Fatal("expected at least one migration")
	}
	for i := 1; i < len(migs); i++ {
		if migs[i].Version < migs[i-1].Version {
			t.Fatalf("migrations not sorted: %d before %d", migs[i-1].Version, migs[i].Version)
		}
	}
	if migs[0].Version != currentSchemaBaseline.Version {
		t.Fatalf("expected first migration to be baseline version %d, got %d", currentSchemaBaseline.Version, migs[0].Version)
	}
}
