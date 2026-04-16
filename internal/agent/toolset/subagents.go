package toolset

import (
	"context"
	"encoding/json"
	"errors"

	"tether/internal/tools"
)

type SubagentSpawn struct{}

type subagentSpawnArgs struct {
	Prompt string `json:"prompt"`
}

func (t SubagentSpawn) Spec() tools.ToolSpec {
	return tools.ToolSpec{
		Name:      "subagent.spawn",
		Summary:   "Spawn a sub-agent run asynchronously.",
		WhenToUse: "Use this for long-running, multi-step work that you don't want to block the main conversation loop (e.g., deep repo analysis).",
		Safety:    "Subagents run with the same tool constraints as the main agent.",
		InputSchema: map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"prompt": map[string]any{"type": "string", "minLength": 1},
			},
			"required": []string{"prompt"},
		},
		OutputSchema: map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties":           map[string]any{"id": map[string]any{"type": "string"}},
			"required":             []string{"id"},
		},
		Examples: []tools.ToolExample{{Title: "Spawn a repo review", Args: map[string]any{"prompt": "Review the repo for tool documentation gaps."}, Result: map[string]any{"id": "run_..."}}},
		Tags:     []string{"async"},
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
	var args subagentSpawnArgs
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return nil, err
	}
	id := s.Subagents.Spawn(s.UserID, args.Prompt)
	return map[string]any{"id": id}, nil
}

type SubagentStatus struct{}

type subagentStatusArgs struct {
	ID string `json:"id"`
}

func (t SubagentStatus) Spec() tools.ToolSpec {
	return tools.ToolSpec{
		Name:      "subagent.status",
		Summary:   "Get status/result for a spawned sub-agent run.",
		WhenToUse: "Use this after subagent.spawn to poll for completion and retrieve the result.",
		Safety:    "Read-only (status retrieval).",
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
				"found":  map[string]any{"type": "boolean"},
				"status": map[string]any{"description": "implementation-defined status/result object"},
			},
			"required": []string{"found"},
		},
		Examples: []tools.ToolExample{{Title: "Check a run", Args: map[string]any{"id": "run_..."}, Result: map[string]any{"found": true, "status": map[string]any{"state": "done"}}}},
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
	st, ok := s.Subagents.Status(args.ID)
	if !ok {
		return map[string]any{"found": false}, nil
	}
	return map[string]any{"found": true, "status": st}, nil
}
