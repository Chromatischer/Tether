package agent

import (
	"os"
	"strings"

	"tether/internal/userspace"
)

func (a *Agent) personalityText(userID int64, agentKey string) string {
	d := userspace.ForUser(a.cfg.Paths.DataDir, userID)
	_ = userspace.Ensure(d)
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
