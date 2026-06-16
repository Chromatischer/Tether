package proactive

import (
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type Rules struct {
	DailyBrief struct {
		Enabled bool   `yaml:"enabled"`
		Time    string `yaml:"time"` // HH:MM (24h) in UTC for now
	} `yaml:"daily_brief"`

	OpenLoops struct {
		Enabled bool   `yaml:"enabled"`
		Time    string `yaml:"time"` // HH:MM (24h) in UTC for now
	} `yaml:"open_loops"`

	Inactivity struct {
		Enabled bool `yaml:"enabled"`
		Minutes int  `yaml:"minutes"`
	} `yaml:"inactivity"`

	// Custom proactive agents. These are sourced exclusively from the folder-based
	// agents/ directory (one folder per agent), not from this YAML. The field is
	// populated at load time and is never read from or written to proactive.yaml.
	Agents []AgentRule `yaml:"-"`
}

type AgentRule struct {
	ID           string `yaml:"id"`
	Enabled      bool   `yaml:"enabled"`
	Instructions string `yaml:"instructions"`

	// Schedule times in HH:MM (UTC for now). If multiple times are set, the agent may run once per time per day.
	ScheduleTimes []string `yaml:"schedule_times"`

	// Built-in app events (e.g. login, user_message, task_changed).
	Events []string `yaml:"events"`

	// Custom action names that can be triggered manually (e.g. via proactive.run or /proactive action <name>).
	Actions []string `yaml:"actions"`

	// Condition gates the agent on a shell predicate (exit 0 = run). When set
	// with no schedule/events/actions, the engine polls the predicate and runs
	// the agent when it passes. When set alongside a schedule/event/action, it
	// is an additional gate evaluated at fire time.
	Condition *Condition `yaml:"condition,omitempty"`

	CooldownMinutes int `yaml:"cooldown_minutes"` // optional
	MaxPerDay       int `yaml:"max_per_day"`      // optional (default 1)
	TimeoutSeconds  int `yaml:"timeout_seconds"`  // optional (default 120)

	// Dir is the host path of this agent's folder, for folder-based agents.
	// Empty for legacy YAML-defined agents. Not serialized.
	Dir string `yaml:"-"`
}

// Condition is a shell predicate that gates a proactive agent. Either an inline
// Command or a Script (a path relative to the agent's folder) is run in the
// no-network sandbox; exit code 0 means the condition is met.
type Condition struct {
	Command string `yaml:"command,omitempty"`
	Script  string `yaml:"script,omitempty"`
	// PollMinutes throttles evaluation for condition-only agents (default 5).
	PollMinutes int `yaml:"poll_minutes,omitempty"`
}

func DefaultRules() Rules {
	var r Rules
	r.DailyBrief.Enabled = true
	r.DailyBrief.Time = "08:00"
	r.OpenLoops.Enabled = true
	r.OpenLoops.Time = "20:00"
	r.Inactivity.Enabled = true
	r.Inactivity.Minutes = 240
	return r
}

func LoadRules(path string) (Rules, error) {
	r := DefaultRules()
	b, err := os.ReadFile(path)
	if err != nil {
		return r, err
	}
	if err := yaml.Unmarshal(b, &r); err != nil {
		return r, err
	}
	if strings.TrimSpace(r.DailyBrief.Time) == "" {
		r.DailyBrief.Time = "08:00"
	}
	if strings.TrimSpace(r.OpenLoops.Time) == "" {
		r.OpenLoops.Time = "20:00"
	}
	return r, nil
}

func DefaultRulesPath(userConfigDir string) string {
	return filepath.Join(userConfigDir, "proactive.yaml")
}
