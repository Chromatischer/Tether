package toolset

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"tether/internal/tools"
)

type ToolSearch struct{}

type toolSearchArgs struct {
	Query string `json:"query"`
}

func (t ToolSearch) Spec() tools.ToolSpec {
	return tools.ToolSpec{
		Name:     "tool.search",
		Category: tools.CategoryTools,
		Summary:  "Search for available tools by name, purpose, tags, and usage hints.",
		WhenToUse: "Use this when you need to discover what capabilities exist (or what a tool is called) before enabling/using it. " +
			"Use natural keyword queries like 'bash shell', 'run command', or 'read files'. " +
			"For full documentation (schemas + examples), call tool.describe.",
		InputSchema: map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"query": map[string]any{"type": "string", "description": "search query; matches keywords across tool names, summaries, tags, and usage hints. Empty = list all"},
			},
		},
		OutputSchema: map[string]any{
			"type": "array",
			"items": map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]any{
					"name":        map[string]any{"type": "string"},
					"description": map[string]any{"type": "string"},
				},
				"required": []string{"name", "description"},
			},
		},
		Examples: []tools.ToolExample{
			{
				Title: "Find web tools",
				Args:  map[string]any{"query": "web"},
				Result: []map[string]any{
					{"name": "web-search", "description": "Search the web and return a small list of results."},
					{"name": "web-fetch", "description": "Fetch a URL over the network and cache the truncated response body."},
				},
				Notes: "Descriptions are short on purpose; call tool.describe for full details.",
			},
		},
		Tags: []string{"meta"},
	}
}

func (t ToolSearch) Definition() ToolDef {
	spec := t.Spec()
	return ToolDef{Name: spec.Name, Description: tools.LLMDescription(spec), Parameters: spec.InputSchema}
}

func (t ToolSearch) Execute(ctx context.Context, s *Session, rawArgs json.RawMessage) (any, error) {
	_ = ctx
	if s.Registry == nil {
		return nil, errors.New("tool registry not configured")
	}
	var args toolSearchArgs
	_ = json.Unmarshal(rawArgs, &args)
	results := s.Registry.Search(args.Query)
	filtered := make([]tools.ToolInfo, 0, len(results))
	for _, info := range results {
		// view_image is only usable on vision-capable models; hide it otherwise.
		if info.Name == "view_image" && !s.VisionEnabled {
			continue
		}
		if s.Allowed != nil && !s.IsAllowed(info.Name) {
			continue
		}
		filtered = append(filtered, info)
	}
	return filtered, nil
}

type ToolEnable struct{}

type toolEnableArgs struct {
	Category     string `json:"category"`
	Network      bool   `json:"network"`
	ConfirmToken string `json:"confirm_token"`
}

func (t ToolEnable) Spec() tools.ToolSpec {
	cats := tools.AllCategories()
	catLines := make([]string, 0, len(cats))
	for _, c := range cats {
		state := "off by default"
		switch {
		case c.AlwaysOn:
			state = "always on"
		case c.DefaultOn:
			state = "on by default"
		}
		catLines = append(catLines, fmt.Sprintf("%s (%s) — %s", c.Name, state, c.Description))
	}
	return tools.ToolSpec{
		Name:     "tool.enable",
		Category: tools.CategoryTools,
		Summary:  "Enable a whole tool category (group) for the current agent session.",
		WhenToUse: "Tools are grouped into categories and enabled by the whole group, not one at a time. " +
			"Use tool.search to find a tool and see its `category`, then enable that category here. " +
			"Categories:\n  " + strings.Join(catLines, "\n  "),
		InputSchema: map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"category":      map[string]any{"type": "string", "enum": tools.CategoryNames(), "description": "category/group to enable (enables every tool in it)"},
				"network":       map[string]any{"type": "boolean", "description": "for category=exec only: request network access for bash this session; requires confirm_token"},
				"confirm_token": map[string]any{"type": "string", "description": "required when enabling exec with network access"},
			},
			"required": []string{"category"},
		},
		OutputSchema: map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"category": map[string]any{"type": "string"},
				"enabled":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "tool names now active"},
				"network":  map[string]any{"type": "boolean"},
			},
			"required": []string{"category", "enabled"},
		},
		Examples: []tools.ToolExample{
			{Title: "Enable the exec group (bash)", Args: map[string]any{"category": "exec"}, Result: map[string]any{"category": "exec", "enabled": []string{"bash"}}},
			{Title: "Enable exec with network access", Args: map[string]any{"category": "exec", "network": true, "confirm_token": "<confirmed-token>"}, Result: map[string]any{"category": "exec", "enabled": []string{"bash"}, "network": true}},
			{Title: "Enable the memory group", Args: map[string]any{"category": "memory"}, Result: map[string]any{"category": "memory", "enabled": []string{"memory.add", "memory.delete", "memory.list", "memory.update"}}},
		},
		Tags: []string{"meta"},
	}
}

func (t ToolEnable) Definition() ToolDef {
	spec := t.Spec()
	return ToolDef{Name: spec.Name, Description: tools.LLMDescription(spec), Parameters: spec.InputSchema}
}

func (t ToolEnable) Execute(ctx context.Context, s *Session, rawArgs json.RawMessage) (any, error) {
	_ = ctx
	var args toolEnableArgs
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return nil, err
	}
	category := strings.TrimSpace(args.Category)
	if args.Network {
		if !strings.EqualFold(category, tools.CategoryExec) {
			return nil, fmt.Errorf("network=true is only supported for category %q", tools.CategoryExec)
		}
		scope := bashEnableNetworkScope()
		if s.Confirm == nil || !s.Confirm.Consume(s.UserID, strings.TrimSpace(args.ConfirmToken), scope) {
			return nil, fmt.Errorf("tool.enable requires confirmation; scope=%q", scope)
		}
		s.BashNetworkEnabled = true
	}
	enabled, err := s.EnableCategory(category)
	if err != nil {
		return nil, err
	}
	return map[string]any{"category": category, "enabled": enabled, "network": args.Network && s.BashNetworkEnabled}, nil
}

type ToolDescribe struct{}

type toolDescribeArgs struct {
	Name string `json:"name"`
}

func (t ToolDescribe) Spec() tools.ToolSpec {
	return tools.ToolSpec{
		Name:      "tool.describe",
		Category:  tools.CategoryTools,
		Summary:   "Get full documentation for a tool (schemas, examples, safety notes).",
		WhenToUse: "Use this whenever you’re about to call a tool and you’re not 100% sure about its arguments, confirmation rules, or output shape.",
		InputSchema: map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"name": map[string]any{"type": "string", "minLength": 1, "description": "tool name (exact)"},
			},
			"required": []string{"name"},
		},
		OutputSchema: map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"name":          map[string]any{"type": "string"},
				"summary":       map[string]any{"type": "string"},
				"when_to_use":   map[string]any{"type": "string"},
				"safety":        map[string]any{"type": "string"},
				"input_schema":  map[string]any{"type": "object", "description": "JSON schema"},
				"output_schema": map[string]any{"description": "JSON schema-ish; documentation only"},
				"examples": map[string]any{
					"type": "array",
					"items": map[string]any{
						"type":                 "object",
						"additionalProperties": true,
					},
				},
				"tags": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			},
			"required": []string{"name", "summary", "input_schema"},
		},
		Examples: []tools.ToolExample{
			{Title: "Describe web-fetch", Args: map[string]any{"name": "web-fetch"}},
		},
		Tags: []string{"meta"},
	}
}

func (t ToolDescribe) Definition() ToolDef {
	spec := t.Spec()
	return ToolDef{Name: spec.Name, Description: tools.LLMDescription(spec), Parameters: spec.InputSchema}
}

func (t ToolDescribe) Execute(ctx context.Context, s *Session, rawArgs json.RawMessage) (any, error) {
	_ = ctx
	if s.Registry == nil {
		return nil, errors.New("tool registry not configured")
	}
	var args toolDescribeArgs
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return nil, err
	}
	name := strings.TrimSpace(args.Name)
	if name == "" {
		return nil, fmt.Errorf("name required")
	}
	if !s.IsAllowed(name) {
		return nil, fmt.Errorf("tool not allowed in this session: %s", name)
	}
	spec, ok := s.Registry.Get(name)
	if !ok {
		return nil, fmt.Errorf("unknown tool: %s", name)
	}
	return spec, nil
}
