package toolset

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"tether/internal/tools"
)

type ConfirmRequest struct{}

type confirmRequestArgs struct {
	Scope        string `json:"scope"`
	Reason       string `json:"reason"`
	ConfirmToken string `json:"confirm_token"`
}

func (t ConfirmRequest) Spec() tools.ToolSpec {
	return tools.ToolSpec{
		Name:    "confirm.request",
		Summary: "Request user confirmation and pause until the user confirms.",
		WhenToUse: "Use this when you need explicit user approval before continuing, especially when you need a confirmation token " +
			"to pass into another tool call (e.g. write overwrite, destructive bash). " +
			"For built-in tool confirmations, the host usually pauses automatically; you only need this tool when you want to ask for approval BEFORE attempting the destructive call.",
		Safety: "This tool does not perform the destructive action itself. It pauses the run until the user confirms via /confirm <token>, " +
			"then returns the token so you can pass it as confirm_token to the actual destructive tool call.",
		InputSchema: map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"scope":         map[string]any{"type": "string", "minLength": 1, "description": "required action scope (must match the tool you plan to run after confirmation)"},
				"reason":        map[string]any{"type": "string", "description": "short user-facing reason shown in the confirmation prompt"},
				"confirm_token": map[string]any{"type": "string", "description": "(host-injected on resume) the confirmed token"},
			},
			"required": []string{"scope"},
		},
		OutputSchema: map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"token":     map[string]any{"type": "string"},
				"scope":     map[string]any{"type": "string"},
				"confirmed": map[string]any{"type": "boolean"},
			},
			"required": []string{"token", "scope", "confirmed"},
		},
		Examples: []tools.ToolExample{
			{
				Title: "Pause for confirmation, then use the token",
				Args:  map[string]any{"scope": "write:overwrite:...", "reason": "Overwriting config/proactive.yaml"},
				Result: map[string]any{
					"token":     "<token after resume>",
					"scope":     "write:overwrite:...",
					"confirmed": true,
				},
				Notes: "On first call (no confirm_token), the host pauses and asks the user to /confirm <token>. After the user confirms, the host resumes the run and replays the tool call with confirm_token injected.",
			},
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

	// First call: ask host to pause and request a token.
	// The host will create a token, show an instruction to the user, and resume by
	// replaying this same tool call with confirm_token injected.
	if strings.TrimSpace(args.ConfirmToken) == "" {
		return nil, fmt.Errorf("confirmation required; scope=%q", scope)
	}

	// Resumed call: return the (now user-confirmed) token so the model can pass it
	// as confirm_token to the actual destructive tool call.
	return map[string]any{
		"token":     strings.TrimSpace(args.ConfirmToken),
		"scope":     scope,
		"confirmed": true,
	}, nil
}
