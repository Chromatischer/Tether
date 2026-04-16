package proactive

import (
	"os"
	"strings"

	"tether/internal/personality"
	"tether/internal/userspace"
)

func (e *Engine) userDirs(userID int64) (userspace.Dirs, bool) {
	if strings.TrimSpace(e.dataDir) == "" {
		return userspace.Dirs{}, false
	}
	d := userspace.ForUser(e.dataDir, userID)
	_ = userspace.Ensure(d)
	return d, true
}

func (e *Engine) personalityText(userID int64, agentKey string) string {
	d, ok := e.userDirs(userID)
	if !ok {
		return ""
	}
	_ = userspace.EnsurePersonalityFile(d, agentKey)
	p, ok := userspace.PersonalityAbsPath(d, agentKey)
	if !ok {
		return ""
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	out := strings.TrimSpace(string(b))
	const max = 8 * 1024
	if len(out) > max {
		out = out[:max] + "\n... (truncated)"
	}
	return out
}

func (e *Engine) prependPersonality(userID int64, agentKey string, prompt string) string {
	p := e.personalityText(userID, agentKey)
	if strings.TrimSpace(p) == "" {
		return prompt
	}
	return "Agent personality:\n" + p + "\n\n" + prompt
}

// ensurePersonalities creates missing personality files for built-ins and configured custom agents.
func (e *Engine) ensurePersonalities(userID int64, rules Rules) {
	if strings.TrimSpace(e.dataDir) == "" {
		return
	}
	d := userspace.ForUser(e.dataDir, userID)
	_ = userspace.Ensure(d)
	_ = userspace.EnsurePersonalityFile(d, personality.AgentChat)
	_ = userspace.EnsurePersonalityFile(d, personality.AgentProactiveDailyBrief)
	_ = userspace.EnsurePersonalityFile(d, personality.AgentProactiveOpenLoops)
	for _, ar := range rules.Agents {
		if id, ok := personality.NormalizeID(ar.ID); ok {
			_ = userspace.EnsurePersonalityFile(d, personality.ProactiveAgentKey(id))
		}
	}
}
