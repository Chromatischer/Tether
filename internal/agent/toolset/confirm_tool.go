package toolset

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"tether/internal/tools"
)

type ConfirmRequest struct{}

type confirmRequestArgs struct {
	Scope  string `json:"scope"`
	Reason string `json:"reason"`
}

func (t ConfirmRequest) Spec() tools.ToolSpec {
	return tools.ToolSpec{
		Name:    "confirm.request",
		Summary: "Request user confirmation for a destructive/irreversible action.",
		WhenToUse: "Use this when another tool reports that it requires confirmation (it will give you an exact scope string). " +
			"Ask the user to run /confirm <token>, then retry the original tool call with confirm_token.",
		Safety: "This tool does not perform the action; it only creates a single-use confirmation token scoped to one specific action.",
		InputSchema: map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"scope":  map[string]any{"type": "string", "minLength": 1, "description": "required action scope, provided by the tool that needs confirmation"},
				"reason": map[string]any{"type": "string", "description": "short user-facing reason"},
			},
			"required": []string{"scope"},
		},
		OutputSchema: map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"token":       map[string]any{"type": "string"},
				"scope":       map[string]any{"type": "string"},
				"instruction": map[string]any{"type": "string"},
			},
			"required": []string{"token", "scope", "instruction"},
		},
		Examples: []tools.ToolExample{
			{Title: "Request confirmation", Args: map[string]any{"scope": "bash:destructive:...", "reason": "You asked me to delete files."}, Result: map[string]any{"token": "...", "scope": "bash:destructive:...", "instruction": "Please confirm by typing: /confirm ..."}},
		},
		Tags: []string{"safety"},
	}
}

func (t ConfirmRequest) Definition() ToolDef {
	spec := t.Spec()
	return ToolDef{Name: spec.Name, Description: tools.LLMDescription(spec), Parameters: spec.InputSchema}
}

func (t ConfirmRequest) Execute(ctx context.Context, s *Session, rawArgs json.RawMessage) (any, error) {
	_ = ctx
	if s.Confirm == nil {
		return nil, errors.New("confirmation system not configured")
	}
	var args confirmRequestArgs
	_ = json.Unmarshal(rawArgs, &args)
	scope := strings.TrimSpace(args.Scope)
	if scope == "" {
		return nil, errors.New("scope required")
	}
	tok := s.Confirm.Request(s.UserID, scope, args.Reason)
	return map[string]any{
		"token":       tok,
		"scope":       scope,
		"instruction": "Please confirm by typing: /confirm " + tok,
	}, nil
}
