package toolset

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"tether/internal/tools"
)

type ConfirmScope struct{}

type confirmScopeArgs struct {
	Tool    string `json:"tool"`
	Path    string `json:"path"`
	Command string `json:"command"`
	Name    string `json:"name"`
	Network bool   `json:"network"`
}

func (t ConfirmScope) Spec() tools.ToolSpec {
	return tools.ToolSpec{
		Name:    "confirm.scope",
		Summary: "Compute the exact confirmation scope string used by a tool action.",
		WhenToUse: "Use this when you want to ask for approval (via confirm.request) BEFORE attempting a destructive tool call. " +
			"It lets you compute the precise scope string that the target tool will later require for confirm_token consumption.",
		Safety: "Read-only helper. Does not create or confirm tokens.",
		InputSchema: map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"tool":    map[string]any{"type": "string", "minLength": 1, "description": "target tool name (currently: write, bash, tool.enable)"},
				"path":    map[string]any{"type": "string", "description": "file path (for tool=write overwrite scope)"},
				"command": map[string]any{"type": "string", "description": "shell command (for tool=bash destructive scope)"},
				"name":    map[string]any{"type": "string", "description": "tool name (for tool=tool.enable)"},
				"network": map[string]any{"type": "boolean", "description": "for tool=tool.enable and name=bash: whether network access is requested"},
			},
			"required": []string{"tool"},
		},
		OutputSchema: map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"scope": map[string]any{"type": "string"},
			},
			"required": []string{"scope"},
		},
		Examples: []tools.ToolExample{
			{
				Title:  "Write overwrite scope",
				Args:   map[string]any{"tool": "write", "path": "config/proactive.yaml"},
				Result: map[string]any{"scope": "write:overwrite:..."},
				Notes:  "Use the returned scope as the scope in confirm.request, then pass the confirmed token as confirm_token to write.",
			},
		},
		Tags: []string{"safety"},
	}
}

func (t ConfirmScope) Definition() ToolDef {
	spec := t.Spec()
	return ToolDef{Name: spec.Name, Description: tools.LLMDescription(spec), Parameters: spec.InputSchema}
}

func (t ConfirmScope) Execute(ctx context.Context, s *Session, rawArgs json.RawMessage) (any, error) {
	_ = ctx
	_ = s
	var args confirmScopeArgs
	_ = json.Unmarshal(rawArgs, &args)

	tool := strings.TrimSpace(strings.ToLower(args.Tool))
	if tool == "" {
		return nil, errors.New("tool required")
	}

	switch tool {
	case "write":
		p := strings.TrimSpace(args.Path)
		if p == "" {
			return nil, errors.New("path required for tool=write")
		}
		return map[string]any{"scope": writeConfirmScope(p)}, nil
	case "bash":
		cmd := strings.TrimSpace(args.Command)
		if cmd == "" {
			return nil, errors.New("command required for tool=bash")
		}
		return map[string]any{"scope": bashConfirmScope(cmd)}, nil
	case "tool.enable":
		name := strings.TrimSpace(args.Name)
		if name == "" {
			return nil, errors.New("name required for tool=tool.enable")
		}
		if strings.EqualFold(name, "bash") && args.Network {
			return map[string]any{"scope": bashEnableNetworkScope()}, nil
		}
		return nil, errors.New("no confirmation scope required for this tool.enable request")
	default:
		return nil, errors.New("unsupported tool (supported: write, bash, tool.enable)")
	}
}
