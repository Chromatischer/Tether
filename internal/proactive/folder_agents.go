package proactive

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// folderAgentYAML is the on-disk schema for agents/<id>/agent.yaml.
type folderAgentYAML struct {
	ID              string     `yaml:"id"`
	Enabled         *bool      `yaml:"enabled"`
	Instructions    string     `yaml:"instructions"`
	ScheduleTimes   []string   `yaml:"schedule_times"`
	Events          []string   `yaml:"events"`
	Actions         []string   `yaml:"actions"`
	Condition       *Condition `yaml:"condition"`
	CooldownMinutes int        `yaml:"cooldown_minutes"`
	MaxPerDay       int        `yaml:"max_per_day"`
	TimeoutSeconds  int        `yaml:"timeout_seconds"`
}

// loadFolderAgents reads folder-based proactive agents from agentsDir. Each
// immediate subdirectory containing an agent.yaml becomes one AgentRule. The
// run instructions come from instructions.md when present, otherwise from the
// instructions field of agent.yaml.
//
// Malformed or unreadable agents are skipped rather than failing the whole
// load, so one bad folder can't disable the rest of a user's proactive setup.
func loadFolderAgents(agentsDir string) []AgentRule {
	agentsDir = strings.TrimSpace(agentsDir)
	if agentsDir == "" {
		return nil
	}
	entries, err := os.ReadDir(agentsDir)
	if err != nil {
		return nil
	}

	out := make([]AgentRule, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(agentsDir, e.Name())
		ar, ok := loadOneFolderAgent(dir, e.Name())
		if !ok {
			continue
		}
		out = append(out, ar)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func loadOneFolderAgent(dir, folderName string) (AgentRule, bool) {
	b, err := os.ReadFile(filepath.Join(dir, "agent.yaml"))
	if err != nil {
		return AgentRule{}, false
	}
	var y folderAgentYAML
	if err := yaml.Unmarshal(b, &y); err != nil {
		return AgentRule{}, false
	}

	id := strings.TrimSpace(y.ID)
	if id == "" {
		id = folderName
	}
	if _, ok := normalizeAgentID(id); !ok {
		return AgentRule{}, false
	}

	enabled := true
	if y.Enabled != nil {
		enabled = *y.Enabled
	}

	instructions := strings.TrimSpace(y.Instructions)
	if md, err := os.ReadFile(filepath.Join(dir, "instructions.md")); err == nil {
		if s := strings.TrimSpace(string(md)); s != "" {
			instructions = s
		}
	}

	return AgentRule{
		ID:              id,
		Enabled:         enabled,
		Instructions:    instructions,
		ScheduleTimes:   y.ScheduleTimes,
		Events:          y.Events,
		Actions:         y.Actions,
		Condition:       y.Condition,
		CooldownMinutes: y.CooldownMinutes,
		MaxPerDay:       y.MaxPerDay,
		TimeoutSeconds:  y.TimeoutSeconds,
		Dir:             dir,
	}, true
}
