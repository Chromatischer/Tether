package db

import (
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

const bootstrapMigrationsTable = `
CREATE TABLE IF NOT EXISTS schema_migrations (
  version INTEGER PRIMARY KEY,
  applied_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
`

type migration struct {
	Version int
	Name    string
	SQL     string
}

func Migrate(db *sql.DB) error {
	if _, err := db.Exec(bootstrapMigrationsTable); err != nil {
		return fmt.Errorf("bootstrap schema_migrations: %w", err)
	}

	applied, err := appliedVersions(db)
	if err != nil {
		return err
	}

	migs, err := loadMigrations()
	if err != nil {
		return err
	}

	for _, m := range migs {
		if applied[m.Version] {
			continue
		}
		if err := applyMigration(db, m); err != nil {
			return err
		}
	}
	return nil
}

func appliedVersions(db *sql.DB) (map[int]bool, error) {
	rows, err := db.Query(`SELECT version FROM schema_migrations ORDER BY version`)
	if err != nil {
		return nil, fmt.Errorf("query schema_migrations: %w", err)
	}
	defer rows.Close()
	m := map[int]bool{}
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		m[v] = true
	}
	return m, rows.Err()
}

func loadMigrations() ([]migration, error) {
	entries, err := fs.Glob(migrationsFS, "migrations/*.sql")
	if err != nil {
		return nil, err
	}
	migs := make([]migration, 0, len(entries))
	for _, name := range entries {
		base := strings.TrimPrefix(name, "migrations/")
		verStr, _, ok := strings.Cut(base, "_")
		if !ok {
			return nil, fmt.Errorf("invalid migration name %q (expected NNNN_name.sql)", base)
		}
		ver, err := strconv.Atoi(verStr)
		if err != nil {
			return nil, fmt.Errorf("invalid migration version in %q: %w", base, err)
		}
		b, err := migrationsFS.ReadFile(name)
		if err != nil {
			return nil, err
		}
		migs = append(migs, migration{Version: ver, Name: base, SQL: string(b)})
	}
	// Apply in increasing version order.
	sort.Slice(migs, func(i, j int) bool { return migs[i].Version < migs[j].Version })
	return migs, nil
}

func applyMigration(db *sql.DB, m migration) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	if _, err := tx.Exec(m.SQL); err != nil {
		return fmt.Errorf("apply migration %s: %w", m.Name, err)
	}
	if _, err := tx.Exec(`INSERT INTO schema_migrations(version) VALUES (?)`, m.Version); err != nil {
		return fmt.Errorf("record schema_migrations %s: %w", m.Name, err)
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return nil
}
