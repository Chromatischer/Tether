package store

import (
	"database/sql"
	"strings"
	"time"
)

const DefaultUpdateCommand = "sudo -n /usr/local/bin/tether-update"

type AppUpdateSettings struct {
	AutoEnabled       bool
	SourceMode        string
	Branch            string
	ScheduleUTC       string
	Command           string
	LastCheckedAt     int64
	LastAvailableRef  string
	LastSuccessfulRef string
	LastRunAt         int64
	UpdatedAt         int64
}

type AppUpdateRun struct {
	ID         int64
	SourceMode string
	SourceRef  string
	Status     string
	Output     string
	StartedAt  int64
	FinishedAt int64
}

func DefaultAppUpdateSettings() AppUpdateSettings {
	return AppUpdateSettings{
		SourceMode:  "release",
		Branch:      "main",
		ScheduleUTC: "03:00",
		Command:     DefaultUpdateCommand,
	}
}

func NormalizeAppUpdateSettings(s AppUpdateSettings) AppUpdateSettings {
	s.SourceMode = strings.TrimSpace(strings.ToLower(s.SourceMode))
	if s.SourceMode != "branch" {
		s.SourceMode = "release"
	}
	s.Branch = strings.TrimSpace(s.Branch)
	if s.Branch == "" {
		s.Branch = "main"
	}
	s.ScheduleUTC = strings.TrimSpace(s.ScheduleUTC)
	if s.ScheduleUTC == "" {
		s.ScheduleUTC = "03:00"
	}
	s.Command = strings.TrimSpace(s.Command)
	if s.Command == "" {
		s.Command = DefaultUpdateCommand
	}
	return s
}

func GetAppUpdateSettings(db *sql.DB) (AppUpdateSettings, error) {
	def := DefaultAppUpdateSettings()
	var auto int
	var lastChecked, lastRun, updated sql.NullInt64
	var lastAvailable, lastSuccessful sql.NullString
	err := db.QueryRow(`
SELECT auto_enabled, source_mode, branch, schedule_utc, command,
       last_checked_at, last_available_ref, last_successful_ref, last_run_at, updated_at
FROM app_update_settings WHERE id=1`,
	).Scan(&auto, &def.SourceMode, &def.Branch, &def.ScheduleUTC, &def.Command, &lastChecked, &lastAvailable, &lastSuccessful, &lastRun, &updated)
	if err == sql.ErrNoRows {
		return def, nil
	}
	if err != nil {
		return def, err
	}
	def.AutoEnabled = auto != 0
	if lastChecked.Valid {
		def.LastCheckedAt = lastChecked.Int64
	}
	if lastAvailable.Valid {
		def.LastAvailableRef = lastAvailable.String
	}
	if lastSuccessful.Valid {
		def.LastSuccessfulRef = lastSuccessful.String
	}
	if lastRun.Valid {
		def.LastRunAt = lastRun.Int64
	}
	if updated.Valid {
		def.UpdatedAt = updated.Int64
	}
	return NormalizeAppUpdateSettings(def), nil
}

func SaveAppUpdateSettings(db *sql.DB, s AppUpdateSettings) error {
	s = NormalizeAppUpdateSettings(s)
	auto := 0
	if s.AutoEnabled {
		auto = 1
	}
	_, err := db.Exec(`
INSERT INTO app_update_settings(
  id, auto_enabled, source_mode, branch, schedule_utc, command,
  last_checked_at, last_available_ref, last_successful_ref, last_run_at, updated_at
) VALUES (1,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET
  auto_enabled=excluded.auto_enabled,
  source_mode=excluded.source_mode,
  branch=excluded.branch,
  schedule_utc=excluded.schedule_utc,
  command=excluded.command,
  last_checked_at=excluded.last_checked_at,
  last_available_ref=excluded.last_available_ref,
  last_successful_ref=excluded.last_successful_ref,
  last_run_at=excluded.last_run_at,
  updated_at=excluded.updated_at`,
		auto, s.SourceMode, s.Branch, s.ScheduleUTC, s.Command,
		nullInt64(s.LastCheckedAt), nullString(s.LastAvailableRef), nullString(s.LastSuccessfulRef), nullInt64(s.LastRunAt), time.Now().Unix())
	return err
}

func InsertAppUpdateRun(db *sql.DB, mode, ref, status, output string, startedAt int64) (int64, error) {
	res, err := db.Exec(`
INSERT INTO app_update_runs(source_mode, source_ref, status, output, started_at)
VALUES (?,?,?,?,?)`, mode, ref, status, output, startedAt)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func FinishAppUpdateRun(db *sql.DB, id int64, status, output string, finishedAt int64) error {
	_, err := db.Exec(`UPDATE app_update_runs SET status=?, output=?, finished_at=? WHERE id=?`, status, output, finishedAt, id)
	return err
}

func ListAppUpdateRuns(db *sql.DB, limit int) ([]AppUpdateRun, error) {
	if limit <= 0 {
		limit = 10
	}
	rows, err := db.Query(`
SELECT id, source_mode, COALESCE(source_ref,''), status, COALESCE(output,''), started_at, COALESCE(finished_at,0)
FROM app_update_runs ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AppUpdateRun{}
	for rows.Next() {
		var r AppUpdateRun
		if err := rows.Scan(&r.ID, &r.SourceMode, &r.SourceRef, &r.Status, &r.Output, &r.StartedAt, &r.FinishedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func nullString(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return s
}

func nullInt64(v int64) any {
	if v == 0 {
		return nil
	}
	return v
}
