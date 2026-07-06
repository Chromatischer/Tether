package proactive

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadFolderAgents(t *testing.T) {
	dir := t.TempDir()
	agentsDir := filepath.Join(dir, "agents")

	// A scheduled agent with instructions.md.
	mk := func(name, yaml, instructions string) {
		ad := filepath.Join(agentsDir, name)
		if err := os.MkdirAll(ad, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(ad, "agent.yaml"), []byte(yaml), 0o644); err != nil {
			t.Fatal(err)
		}
		if instructions != "" {
			if err := os.WriteFile(filepath.Join(ad, "instructions.md"), []byte(instructions), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}

	mk("morning", "schedule_times: [\"08:00\"]\nmax_per_day: 2\n", "Summarize the day.")
	mk("watcher", "condition:\n  script: check.sh\n  poll_minutes: 3\n", "Alert when triggered.")
	mk("disabled", "enabled: false\nschedule_times: [\"09:00\"]\n", "Should be disabled.")
	// A folder without agent.yaml must be ignored.
	if err := os.MkdirAll(filepath.Join(agentsDir, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}

	got := loadFolderAgents(agentsDir)
	if len(got) != 3 {
		t.Fatalf("expected 3 agents, got %d", len(got))
	}

	byID := map[string]AgentRule{}
	for _, a := range got {
		byID[a.ID] = a
	}

	morning, ok := byID["morning"]
	if !ok {
		t.Fatal("missing morning agent")
	}
	if !morning.Enabled {
		t.Error("morning should default to enabled")
	}
	if morning.Instructions != "Summarize the day." {
		t.Errorf("morning instructions = %q", morning.Instructions)
	}
	if len(morning.ScheduleTimes) != 1 || morning.ScheduleTimes[0] != "08:00" {
		t.Errorf("morning schedule = %v", morning.ScheduleTimes)
	}
	if morning.MaxPerDay != 2 {
		t.Errorf("morning max_per_day = %d", morning.MaxPerDay)
	}
	if morning.Dir == "" {
		t.Error("morning Dir should be set")
	}

	watcher := byID["watcher"]
	if watcher.Condition == nil || watcher.Condition.Script != "check.sh" || watcher.Condition.PollMinutes != 3 {
		t.Errorf("watcher condition = %+v", watcher.Condition)
	}

	if byID["disabled"].Enabled {
		t.Error("disabled agent should be disabled")
	}
}

func TestConditionCommand_ScriptPathMapping(t *testing.T) {
	dir := t.TempDir()
	e := NewEngine(nil, nil, nil, nil, dir)
	agentDir := filepath.Join(dir, "users", "7", "agents", "watcher")

	ar := AgentRule{
		ID:        "watcher",
		Dir:       agentDir,
		Condition: &Condition{Script: "check.sh"},
	}
	cmd, err := e.conditionCommand(7, ar)
	if err != nil {
		t.Fatal(err)
	}
	want := "bash '/work/agents/watcher/check.sh'"
	if cmd != want {
		t.Errorf("conditionCommand = %q, want %q", cmd, want)
	}

	// Inline command wins and is returned verbatim.
	ar2 := AgentRule{ID: "x", Condition: &Condition{Command: "test -f /work/workspace/flag"}}
	cmd2, err := e.conditionCommand(7, ar2)
	if err != nil || cmd2 != "test -f /work/workspace/flag" {
		t.Errorf("inline conditionCommand = %q, err=%v", cmd2, err)
	}

	// Path traversal must be rejected.
	ar3 := AgentRule{ID: "x", Dir: agentDir, Condition: &Condition{Script: "../../escape.sh"}}
	if _, err := e.conditionCommand(7, ar3); err == nil {
		t.Error("expected error for traversal script path")
	}
}
