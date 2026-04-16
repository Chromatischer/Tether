package toolset

import (
	"context"
	"encoding/json"
	"errors"
)

type SubagentSpawn struct{}

type subagentSpawnArgs struct {
	Prompt string `json:"prompt"`
}

func (t SubagentSpawn) Definition() ToolDef {
	return ToolDef{
		Name:        "subagent.spawn",
		Description: "Spawn a sub-agent run asynchronously. Returns an id.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"prompt": map[string]any{"type": "string"},
			},
			"required": []string{"prompt"},
		},
	}
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

func (t SubagentStatus) Definition() ToolDef {
	return ToolDef{
		Name:        "subagent.status",
		Description: "Get status/result for a previously spawned sub-agent run.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id": map[string]any{"type": "string"},
			},
			"required": []string{"id"},
		},
	}
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
