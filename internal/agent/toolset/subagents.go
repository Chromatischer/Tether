package toolset

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"tether/internal/subagents"
	"tether/internal/tools"
)

type SubagentSpawn struct{}

type subagentSpawnArgs struct {
	Prompt         string   `json:"prompt"`
	AllowedTools   []string `json:"allowed_tools"`
	PreloadSkill   string   `json:"preload_skill"`
	SkillArguments string   `json:"skill_arguments"`
}

func (t SubagentSpawn) Spec() tools.ToolSpec {
	return tools.ToolSpec{
		Name:      "subagent.spawn",
		Summary:   "Spawn a constrained background sub-agent run asynchronously with a caller-selected toolset and at most one preloaded skill.",
		WhenToUse: "Use this for long-running, multi-step work that should continue in the background without blocking the main conversation loop (e.g., deep repo analysis). After spawning it, you can either keep working and interacting with the user while it runs, or poll subagent.status until it finishes if its result is on your critical path. You must decide the subagent's toolset up front and should keep it as narrow as possible for the task.",
		Safety:    "Subagents are intentionally constrained: the spawning agent chooses the exact tools they may use, they cannot spawn further subagents, and they cannot invoke new skills after launch. This keeps delegation bounded, prevents recursive agent trees, and avoids uncontrolled skill/tool expansion inside background runs.",
		InputSchema: map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"prompt":          map[string]any{"type": "string", "minLength": 1, "description": "The task for the subagent."},
				"allowed_tools":   map[string]any{"type": "array", "description": "Exact tool names the subagent may use for this run. Decide this at spawn time. Keep it minimal. Tools outside this list cannot be enabled later.", "items": map[string]any{"type": "string", "minLength": 1}},
				"preload_skill":   map[string]any{"type": "string", "description": "Optional single skill name to inject before the subagent starts. Subagents cannot invoke additional skills later."},
				"skill_arguments": map[string]any{"type": "string", "description": "Optional arguments passed when preloading the single allowed skill."},
			},
			"required": []string{"prompt"},
		},
		OutputSchema: map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties":           map[string]any{"id": map[string]any{"type": "string"}},
			"required":             []string{"id"},
		},
		Examples: []tools.ToolExample{
			{
				Title:  "Spawn a repo review",
				Args:   map[string]any{"prompt": "Review the repo for tool documentation gaps.", "allowed_tools": []string{"read", "bash"}},
				Result: map[string]any{"id": "run_..."},
				Notes:  "The subagent may use only read and bash for this run. It cannot spawn other subagents or load more skills.",
			},
		},
		Tags: []string{"async"},
	}
}

func (t SubagentSpawn) Definition() ToolDef {
	spec := t.Spec()
	return ToolDef{Name: spec.Name, Description: tools.LLMDescription(spec), Parameters: spec.InputSchema}
}

func (t SubagentSpawn) Execute(ctx context.Context, s *Session, rawArgs json.RawMessage) (any, error) {
	_ = ctx
	if s.Subagents == nil {
		return nil, errors.New("subagent system not configured")
	}
	if s.IsSubagent {
		return nil, errors.New("subagents cannot spawn subagents")
	}
	if s.Registry == nil {
		return nil, errors.New("tool registry not configured")
	}
	var args subagentSpawnArgs
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return nil, err
	}
	allowed, err := validateSubagentToolset(s, args.AllowedTools)
	if err != nil {
		return nil, err
	}
	req := SubagentSpawnRequest{
		Prompt:       strings.TrimSpace(args.Prompt),
		AllowedTools: allowed,
	}
	if skill := strings.TrimSpace(args.PreloadSkill); skill != "" {
		req.Skill = &SubagentSkill{Name: skill, Arguments: strings.TrimSpace(args.SkillArguments)}
	}
	id := s.Subagents.Spawn(s.UserID, req)
	return map[string]any{"id": id}, nil
}

type SubagentStatus struct{}

type subagentStatusArgs struct {
	ID string `json:"id"`
}

func (t SubagentStatus) Spec() tools.ToolSpec {
	return tools.ToolSpec{
		Name:      "subagent.status",
		Summary:   "Get live status for a spawned sub-agent run, including current state and recent progress history.",
		WhenToUse: "Use this after subagent.spawn when the subagent is running in the background and you want to inspect its current status, latest text, or recent tool/activity history without blocking the main conversation. Poll it when you need to wait for completion; otherwise continue working and check back later.",
		Safety:    "Read-only. This lets the main agent either monitor background work while continuing to interact with the user or explicitly poll until the subagent finishes.",
		InputSchema: map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"id": map[string]any{"type": "string", "minLength": 1},
			},
			"required": []string{"id"},
		},
		OutputSchema: map[string]any{
			"type":                 "object",
			"additionalProperties": true,
			"properties": map[string]any{
				"found": map[string]any{"type": "boolean"},
				"status": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"id":           map[string]any{"type": "string"},
						"state":        map[string]any{"type": "string"},
						"current_text": map[string]any{"type": "string"},
						"result":       map[string]any{"type": "string"},
						"error":        map[string]any{"type": "string"},
						"history": map[string]any{
							"type": "array",
							"items": map[string]any{
								"type": "object",
								"properties": map[string]any{
									"at":   map[string]any{"type": "string"},
									"type": map[string]any{"type": "string"},
									"text": map[string]any{"type": "string"},
								},
							},
						},
					},
				},
			},
			"required": []string{"found"},
		},
		Examples: []tools.ToolExample{{Title: "Check a running subagent", Args: map[string]any{"id": "run_..."}, Result: map[string]any{"found": true, "status": map[string]any{"status": "running", "current_text": "Reviewing the repo layout", "history": []map[string]any{{"type": "tool_call", "text": "Tool calling: read"}}}}}},
		Tags:     []string{"async"},
	}
}

func (t SubagentStatus) Definition() ToolDef {
	spec := t.Spec()
	return ToolDef{Name: spec.Name, Description: tools.LLMDescription(spec), Parameters: spec.InputSchema}
}

func (t SubagentStatus) Execute(ctx context.Context, s *Session, rawArgs json.RawMessage) (any, error) {
	_ = ctx
	if s.Subagents == nil {
		return nil, errors.New("subagent system not configured")
	}
	var args subagentStatusArgs
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return nil, err
	}
	st, ok := s.Subagents.Status(s.UserID, args.ID)
	if !ok {
		return map[string]any{"found": false}, nil
	}
	return map[string]any{"found": true, "status": summarizeSubagentStatus(st)}, nil
}

func validateSubagentToolset(s *Session, requested []string) ([]string, error) {
	seen := map[string]bool{}
	allowed := make([]string, 0, len(requested))
	for _, name := range requested {
		name = strings.TrimSpace(name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		if name == "subagent.spawn" || name == "subagent.status" {
			return nil, fmt.Errorf("subagent tool not allowed: %s", name)
		}
		if name == "skill.invoke" {
			return nil, errors.New("subagents cannot invoke skills after spawn")
		}
		if _, ok := s.Registry.Get(name); !ok {
			return nil, fmt.Errorf("unknown tool: %s", name)
		}
		if !s.IsAllowed(name) {
			return nil, fmt.Errorf("tool not allowed in this session: %s", name)
		}
		allowed = append(allowed, name)
	}
	return allowed, nil
}

func summarizeSubagentStatus(st any) map[string]any {
	switch run := st.(type) {
	case *subagents.Run:
		return subagentStatusMap(run)
	case subagents.Run:
		return subagentStatusMap(&run)
	default:
		return map[string]any{"raw": st}
	}
}

func subagentStatusMap(run *subagents.Run) map[string]any {
	if run == nil {
		return map[string]any{}
	}
	history := make([]map[string]any, 0, len(run.History))
	for _, entry := range run.History {
		history = append(history, map[string]any{
			"at":   entry.At.UTC().Format(time.RFC3339),
			"type": entry.Type,
			"text": entry.Text,
		})
	}
	out := map[string]any{
		"id":           run.ID,
		"state":        string(run.Status),
		"current_text": run.CurrentText,
		"history":      history,
	}
	if !run.CreatedAt.IsZero() {
		out["created_at"] = run.CreatedAt.UTC().Format(time.RFC3339)
	}
	if !run.StartedAt.IsZero() {
		out["started_at"] = run.StartedAt.UTC().Format(time.RFC3339)
	}
	if !run.UpdatedAt.IsZero() {
		out["updated_at"] = run.UpdatedAt.UTC().Format(time.RFC3339)
	}
	if !run.EndedAt.IsZero() {
		out["ended_at"] = run.EndedAt.UTC().Format(time.RFC3339)
	}
	if strings.TrimSpace(run.Result) != "" {
		out["result"] = run.Result
	}
	if strings.TrimSpace(run.Err) != "" {
		out["error"] = run.Err
	}
	return out
}
