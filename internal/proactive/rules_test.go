package proactive

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultRules(t *testing.T) {
	r := DefaultRules()
	if !r.DailyBrief.Enabled || r.DailyBrief.Time != "08:00" {
		t.Fatalf("unexpected daily brief defaults: %+v", r.DailyBrief)
	}
	if !r.OpenLoops.Enabled || r.OpenLoops.Time != "20:00" {
		t.Fatalf("unexpected open loops defaults: %+v", r.OpenLoops)
	}
	if !r.Inactivity.Enabled || r.Inactivity.Minutes != 240 {
		t.Fatalf("unexpected inactivity defaults: %+v", r.Inactivity)
	}
}

func TestLoadRules_MergesAndFillsTimes(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "proactive.yaml")
	// Explicitly clear times in file; loader should fill defaults.
	if err := os.WriteFile(p, []byte("daily_brief:\n  enabled: false\n  time: ''\nopen_loops:\n  time: ''\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r, err := LoadRules(p)
	if err != nil {
		t.Fatal(err)
	}
	if r.DailyBrief.Enabled {
		t.Fatalf("expected daily_brief.enabled=false")
	}
	if r.DailyBrief.Time != "08:00" {
		t.Fatalf("expected daily_brief.time default 08:00, got %q", r.DailyBrief.Time)
	}
	if r.OpenLoops.Time != "20:00" {
		t.Fatalf("expected open_loops.time default 20:00, got %q", r.OpenLoops.Time)
	}
}

func TestDefaultRulesPath(t *testing.T) {
	p := DefaultRulesPath("/tmp/u")
	if p != "/tmp/u/proactive.yaml" {
		t.Fatalf("unexpected path: %q", p)
	}
}
