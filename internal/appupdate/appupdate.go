package appupdate

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"time"

	"tether/internal/store"
)

const githubRepo = "Chromatischer/Tether"
const restartCommand = "sudo -n /usr/local/bin/tether-restart"

var runMu sync.Mutex

type Available struct {
	Mode string
	Ref  string
}

func Check(ctx context.Context, s store.AppUpdateSettings) (Available, error) {
	s = store.NormalizeAppUpdateSettings(s)
	switch s.SourceMode {
	case "branch":
		sha, err := latestBranchSHA(ctx, s.Branch)
		return Available{Mode: "branch", Ref: sha}, err
	default:
		tag, err := latestReleaseTag(ctx)
		return Available{Mode: "release", Ref: tag}, err
	}
}

func CheckAndRecord(ctx context.Context, db *sql.DB) (Available, store.AppUpdateSettings, error) {
	s, err := store.GetAppUpdateSettings(db)
	if err != nil {
		return Available{}, s, err
	}
	avail, err := Check(ctx, s)
	now := time.Now().Unix()
	s.LastCheckedAt = now
	if err == nil {
		s.LastAvailableRef = avail.Ref
	}
	if saveErr := store.SaveAppUpdateSettings(db, s); saveErr != nil && err == nil {
		err = saveErr
	}
	return avail, s, err
}

func Run(ctx context.Context, db *sql.DB, manual bool) error {
	runMu.Lock()
	defer runMu.Unlock()

	s, err := store.GetAppUpdateSettings(db)
	if err != nil {
		return err
	}
	avail, err := Check(ctx, s)
	now := time.Now().Unix()
	s.LastCheckedAt = now
	if err != nil {
		_ = store.SaveAppUpdateSettings(db, s)
		return err
	}
	s.LastAvailableRef = avail.Ref
	if !manual && s.LastSuccessfulRef == avail.Ref {
		return store.SaveAppUpdateSettings(db, s)
	}

	runID, _ := store.InsertAppUpdateRun(db, s.SourceMode, avail.Ref, "running", "", now)
	output, runErr := runUpdater(ctx, s)
	finished := time.Now().Unix()
	status := "success"
	if runErr != nil {
		status = "failed"
		output = strings.TrimSpace(output + "\n" + runErr.Error())
	} else {
		s.LastSuccessfulRef = avail.Ref
	}
	s.LastRunAt = finished
	s.LastAvailableRef = avail.Ref
	_ = store.SaveAppUpdateSettings(db, s)
	if runID != 0 {
		_ = store.FinishAppUpdateRun(db, runID, status, truncate(output, 8000), finished)
	}
	if runErr == nil {
		startRestart()
	}
	return runErr
}

func AutoDue(s store.AppUpdateSettings, now time.Time) bool {
	s = store.NormalizeAppUpdateSettings(s)
	if !s.AutoEnabled || !dueAt(now.UTC(), s.ScheduleUTC) {
		return false
	}
	if s.LastRunAt == 0 {
		return true
	}
	last := time.Unix(s.LastRunAt, 0).UTC()
	return last.Format("2006-01-02") != now.UTC().Format("2006-01-02")
}

func runUpdater(ctx context.Context, s store.AppUpdateSettings) (string, error) {
	args := []string{"--no-restart"}
	if s.SourceMode == "branch" {
		args = append(args, "--branch", s.Branch)
	}
	parts := strings.Fields(s.Command)
	if len(parts) == 0 {
		parts = strings.Fields(store.DefaultUpdateCommand)
	}
	cmd := exec.CommandContext(ctx, parts[0], append(parts[1:], args...)...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	return strings.TrimSpace(out.String()), err
}

func startRestart() {
	parts := strings.Fields(restartCommand)
	if len(parts) == 0 {
		return
	}
	_ = exec.Command(parts[0], parts[1:]...).Start()
}

func latestReleaseTag(ctx context.Context) (string, error) {
	var body struct {
		TagName string `json:"tag_name"`
	}
	if err := getJSON(ctx, "https://api.github.com/repos/"+githubRepo+"/releases/latest", &body); err != nil {
		return "", err
	}
	if strings.TrimSpace(body.TagName) == "" {
		return "", fmt.Errorf("latest release response did not include tag_name")
	}
	return strings.TrimSpace(body.TagName), nil
}

func latestBranchSHA(ctx context.Context, branch string) (string, error) {
	var body struct {
		SHA string `json:"sha"`
	}
	if err := getJSON(ctx, "https://api.github.com/repos/"+githubRepo+"/commits/"+branch, &body); err != nil {
		return "", err
	}
	if strings.TrimSpace(body.SHA) == "" {
		return "", fmt.Errorf("branch response did not include sha")
	}
	return strings.TrimSpace(body.SHA), nil
}

func getJSON(ctx context.Context, url string, dst any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("github returned HTTP %d", resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(dst)
}

func dueAt(now time.Time, hhmm string) bool {
	parts := strings.Split(strings.TrimSpace(hhmm), ":")
	if len(parts) != 2 {
		return false
	}
	hh, ok := atoi2(parts[0])
	if !ok {
		return false
	}
	mm, ok := atoi2(parts[1])
	if !ok {
		return false
	}
	return now.Hour() == hh && now.Minute() == mm
}

func atoi2(s string) (int, bool) {
	if len(s) != 2 {
		return 0, false
	}
	v := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, false
		}
		v = v*10 + int(r-'0')
	}
	return v, true
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "\n...(truncated)"
}
