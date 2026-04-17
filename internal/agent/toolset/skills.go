package toolset

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"tether/internal/skills"
	"tether/internal/tools"
)

// SkillInvoke loads a Claude Code–style skill (SKILL.md) into the current session.
// The skill content is returned and also stored in-memory so it is re-attached on later turns.
type SkillInvoke struct{}

type skillInvokeArgs struct {
	Name         string `json:"name"`
	Arguments    string `json:"arguments"`
	ConfirmToken string `json:"confirm_token"`
}

func (t SkillInvoke) Spec() tools.ToolSpec {
	return tools.ToolSpec{
		Name:    "skill.invoke",
		Summary: "Load and apply a Claude Code–style skill by name.",
		WhenToUse: "Use this when a skill’s description matches the user’s request, or when the user explicitly invokes $<skill-name>. " +
			"This tool loads the full SKILL.md content (with substitutions and shell injections) into the session so it remains in context.",
		InputSchema: map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"name":          map[string]any{"type": "string", "description": "skill name (e.g. simplify)"},
				"arguments":     map[string]any{"type": "string", "description": "arguments passed to the skill"},
				"confirm_token": map[string]any{"type": "string", "description": "required if the skill uses destructive shell injections"},
			},
			"required": []string{"name"},
		},
		OutputSchema: map[string]any{
			"type":                 "object",
			"additionalProperties": true,
			"properties": map[string]any{
				"name":         map[string]any{"type": "string"},
				"content":      map[string]any{"type": "string"},
				"skill_dir":    map[string]any{"type": "string"},
				"enabledTools": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			},
		},
		Examples: []tools.ToolExample{
			{
				Title:  "Invoke a skill",
				Args:   map[string]any{"name": "simplify", "arguments": "Rewrite the following more clearly: ..."},
				Result: map[string]any{"name": "simplify", "content": "..."},
				Notes:  "The returned content is injected into the session so it remains in context.",
			},
		},
		Tags: []string{"skills"},
	}
}

func (t SkillInvoke) Definition() ToolDef {
	spec := t.Spec()
	return ToolDef{Name: spec.Name, Description: tools.LLMDescription(spec), Parameters: spec.InputSchema}
}

func (t SkillInvoke) Execute(ctx context.Context, s *Session, rawArgs json.RawMessage) (any, error) {
	if s == nil {
		return nil, errors.New("session not configured")
	}
	var args skillInvokeArgs
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return nil, err
	}
	name := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(args.Name, "/"), "$"))
	if name == "" {
		return nil, errors.New("name required")
	}

	mgr := skills.NewManager()
	list, err := mgr.List(s.Dirs)
	if err != nil {
		return nil, err
	}
	skill, ok := mgr.Resolve(list, name)
	if !ok {
		return nil, errors.New("skill not found: " + name)
	}

	inv, err := mgr.Invoke(ctx, s.Dirs, s.Confirm, skill, skills.InvokeOptions{
		UserID:       s.UserID,
		Arguments:    args.Arguments,
		Invoker:      "model",
		SessionID:    s.SkillSessionID,
		ConfirmToken: args.ConfirmToken,
	})
	if err != nil {
		return nil, err
	}

	// Persist in memory for later turns.
	s.AddInvokedSkill(skill.Name, inv.Content)

	// Best-effort: enable tools mentioned in allowed-tools.
	enabled := []string{}
	for _, toolName := range mapAllowedTools(skill.AllowedTools) {
		if !s.IsActive(toolName) {
			_ = s.Enable(toolName)
		}
		enabled = append(enabled, toolName)
	}

	return map[string]any{
		"name":          skill.Name,
		"content":       inv.Content,
		"skill_dir":     inv.SkillDirSandboxAbs,
		"enabled_tools": enabled,
	}, nil
}

func mapAllowedTools(allowed []string) []string {
	// Claude Code uses names like Bash(...), Read, Write, Grep, Glob.
	// Tether tools are lowercase: bash/read/write/web-search/web-fetch.
	out := []string{}
	seen := map[string]bool{}
	for _, a := range allowed {
		a = strings.TrimSpace(a)
		if a == "" {
			continue
		}
		// Extract leading identifier before '(' if present.
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
