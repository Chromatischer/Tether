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

	// Custom proactive agents.
	// These can run on schedules, on built-in app events, and on manually-triggered actions.
	Agents []AgentRule `yaml:"agents"`
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

	CooldownMinutes int `yaml:"cooldown_minutes"` // optional
	MaxPerDay       int `yaml:"max_per_day"`      // optional (default 1)
	TimeoutSeconds  int `yaml:"timeout_seconds"`  // optional (default 120)
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
