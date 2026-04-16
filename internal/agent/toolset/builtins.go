package toolset

import (
	"context"
	"encoding/json"

	"tether/internal/tools"
)

type ToolSearch struct{}

type toolSearchArgs struct {
	Query string `json:"query"`
}

func (t ToolSearch) Definition() ToolDef {
	return ToolDef{
		Name:        "tool.search",
		Description: "Search for available tools and return matches (name + short description).",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{"type": "string"},
			},
		},
	}
}

func (t ToolSearch) Execute(ctx context.Context, s *Session, rawArgs json.RawMessage) (any, error) {
	_ = ctx
	var args toolSearchArgs
	_ = json.Unmarshal(rawArgs, &args)
	matches := s.Registry.Search(args.Query)
	out := make([]tools.ToolInfo, 0, len(matches))
	for _, m := range matches {
		out = append(out, m)
	}
	return out, nil
}

type ToolEnable struct{}

type toolEnableArgs struct {
	Name string `json:"name"`
}

func (t ToolEnable) Definition() ToolDef {
	return ToolDef{
		Name:        "tool.enable",
		Description: "Enable a tool for the current agent session so it can be used in subsequent steps.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name": map[string]any{"type": "string"},
			},
			"required": []string{"name"},
		},
	}
}

func (t ToolEnable) Execute(ctx context.Context, s *Session, rawArgs json.RawMessage) (any, error) {
	_ = ctx
	var args toolEnableArgs
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return nil, err
	}
	if err := s.Enable(args.Name); err != nil {
		return nil, err
	}
	return map[string]any{"enabled": args.Name}, nil
}
