package agent

import (
	"context"
	"errors"
	"strings"

	"tether/internal/skills"
)

// InvokeSkill loads a Claude Code–style skill and stores it in the in-memory session.
// invoker should be "user" or "model".
func (a *Agent) InvokeSkill(ctx context.Context, userID, convID int64, skillName string, arguments string, confirmToken string, invoker string) (skills.Invocation, error) {
	s := a.sessionFor(userID, convID)
	mgr := skills.NewManager()
	list, err := mgr.List(s.Dirs)
	if err != nil {
		return skills.Invocation{}, err
	}
	skill, ok := mgr.Resolve(list, skillName)
	if !ok {
		return skills.Invocation{}, errors.New("skill not found: " + strings.TrimPrefix(skillName, "/"))
	}

	inv, err := mgr.Invoke(ctx, s.Dirs, s.Confirm, skill, skills.InvokeOptions{
		UserID:       userID,
		Arguments:    arguments,
		Invoker:      invoker,
		SessionID:    s.SkillSessionID,
		ConfirmToken: confirmToken,
	})
	if err != nil {
		return skills.Invocation{}, err
	}

	s.AddInvokedSkill(skill.Name, inv.Content)

	// Best-effort: enable tools named in allowed-tools.
	for _, toolName := range mapAllowedToolsToTether(skill.AllowedTools) {
		if !s.IsActive(toolName) {
			_ = s.Enable(toolName)
		}
	}

	return inv, nil
}

func mapAllowedToolsToTether(allowed []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, a := range allowed {
		a = strings.TrimSpace(a)
		if a == "" {
			continue
		}
		base := a
		if i := strings.Index(base, "("); i != -1 {
			base = base[:i]
		}
		base = strings.ToLower(strings.TrimSpace(base))
		mapped := ""
		switch base {
		case "bash":
			mapped = "bash"
		case "read":
			mapped = "read"
		case "write":
			mapped = "write"
		case "web-search", "websearch", "search":
			mapped = "web-search"
		case "web-fetch", "webfetch", "fetch":
			mapped = "web-fetch"
		}
		if mapped != "" && !seen[mapped] {
			seen[mapped] = true
			out = append(out, mapped)
		}
	}
	return out
}
