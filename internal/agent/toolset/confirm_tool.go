package toolset

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
)

type ConfirmRequest struct{}

type confirmRequestArgs struct {
	Scope  string `json:"scope"`
	Reason string `json:"reason"`
}

func (t ConfirmRequest) Definition() ToolDef {
	return ToolDef{
		Name:        "confirm.request",
		Description: "Request user confirmation for a destructive action. Returns a token the user must confirm via /confirm <token>.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"scope":  map[string]any{"type": "string", "description": "required action scope, provided by the tool that needs confirmation"},
				"reason": map[string]any{"type": "string"},
			},
			"required": []string{"scope"},
		},
	}
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
