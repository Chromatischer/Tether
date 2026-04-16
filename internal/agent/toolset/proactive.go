package toolset

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"tether/internal/proactive"
	"tether/internal/store"
)

type ProactiveRun struct{}

type proactiveRunArgs struct {
	Action  string `json:"action"`
	AgentID string `json:"agent_id"`
}

func (t ProactiveRun) Definition() ToolDef {
	return ToolDef{
		Name:        "proactive.run",
		Description: "Run proactive agents for the current user (built-ins, or custom agents by action/agent_id).",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action":   map[string]any{"type": "string", "description": "Custom action name to trigger matching proactive agents."},
				"agent_id": map[string]any{"type": "string", "description": "Run a single proactive agent by id."},
			},
			"additionalProperties": false,
		},
	}
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

	eng := proactive.NewEngine(s.DB, s.LLM, nil, "")
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
