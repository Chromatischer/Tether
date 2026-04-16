package toolset

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"

	"tether/internal/proactive"
	"tether/internal/store"
	"tether/internal/tools"
)

type ProactiveRun struct{}

type proactiveRunArgs struct {
	Action  string `json:"action"`
	AgentID string `json:"agent_id"`
}

func (t ProactiveRun) Spec() tools.ToolSpec {
	return tools.ToolSpec{
		Name:    "proactive.run",
		Summary: "Run proactive agents for the current user.",
		WhenToUse: "Use this to trigger proactive checks on-demand (e.g., run the daily brief now). " +
			"If no arguments are provided, it runs the built-in daily brief for the user's first conversation.",
		Safety: "Read/analyze oriented; may generate notifications in other parts of the system when used by the scheduler. When called as a tool, it returns results only.",
		InputSchema: map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"action":   map[string]any{"type": "string", "description": "custom action name to trigger matching proactive agents"},
				"agent_id": map[string]any{"type": "string", "description": "run a single proactive agent by id"},
			},
		},
		OutputSchema: map[string]any{
			"oneOf": []any{
				map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"brief": map[string]any{"type": "string"},
					},
					"required": []string{"brief"},
				},
				map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"results": map[string]any{"type": "array"},
					},
					"required": []string{"results"},
				},
			},
		},
		Examples: []tools.ToolExample{
			{Title: "Run built-in daily brief", Args: map[string]any{}, Result: map[string]any{"brief": "..."}},
		},
		Tags: []string{"proactive"},
	}
}

func (t ProactiveRun) Definition() ToolDef {
	spec := t.Spec()
	return ToolDef{Name: spec.Name, Description: tools.LLMDescription(spec), Parameters: spec.InputSchema}
}

func (t ProactiveRun) Execute(ctx context.Context, s *Session, rawArgs json.RawMessage) (any, error) {
	if s.DB == nil {
		return nil, errors.New("db not available")
	}
	if s.LLM == nil {
		return nil, errors.New("llm not available")
	}

	var args proactiveRunArgs
	if len(rawArgs) > 0 {
		_ = json.Unmarshal(rawArgs, &args)
	}

	// Backwards compatible default: run the built-in daily brief now.
	if strings.TrimSpace(args.Action) == "" && strings.TrimSpace(args.AgentID) == "" {
		convID, ok, err := store.FirstConversationID(s.DB, s.UserID)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, errors.New("no conversation found")
		}
		brief, err := proactive.RunOnce(ctx, s.DB, s.LLM, s.UserID, convID)
		if err != nil {
			return nil, err
		}
		return map[string]any{"brief": brief}, nil
	}

	// Best-effort: derive the base dataDir from the per-user sandbox root so proactive
	// runs can load per-user config (including personality files).
	dataDir := ""
	if strings.TrimSpace(s.Dirs.Root) != "" {
		dataDir = filepath.Clean(filepath.Join(s.Dirs.Root, "..", ".."))
	}
	eng := proactive.NewEngine(s.DB, s.LLM, nil, dataDir)
	if strings.TrimSpace(args.AgentID) != "" {
		res, err := eng.RunAgentNow(ctx, s.UserID, args.AgentID, nil)
		if err != nil {
			return nil, err
		}
		return map[string]any{"results": []proactive.AgentRunResult{res}}, nil
	}

	results, err := eng.RunActionNow(ctx, s.UserID, args.Action, nil)
	if err != nil {
		return nil, err
	}
	return map[string]any{"results": results}, nil
}
