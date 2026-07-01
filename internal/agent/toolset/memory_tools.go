package toolset

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"tether/internal/store"
	"tether/internal/tools"
)

type MemoryList struct{}

type memoryListArgs struct {
	Kind  string `json:"kind"`
	Limit int    `json:"limit"`
}

func (t MemoryList) Spec() tools.ToolSpec {
	return tools.ToolSpec{
		Name:    "memory.list",
		Summary: "List memory items (facts/preferences/tasks).",
		Safety:  "Read-only.",
		InputSchema: map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"kind": map[string]any{
					"type":        "string",
					"description": "filter by kind; empty = all",
					"enum":        []string{"", "fact", "pref", "task"},
				},
				"limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 200, "description": "max items (default 100)"},
			},
		},
		OutputSchema: map[string]any{
			"type": "array",
			"items": map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]any{
					"id":         map[string]any{"type": "integer"},
					"kind":       map[string]any{"type": "string", "enum": []string{"fact", "pref", "task"}},
					"content":    map[string]any{"type": "string"},
					"updated_at": map[string]any{"type": "string"},
				},
				"required": []string{"id", "kind", "content", "updated_at"},
			},
		},
		Examples: []tools.ToolExample{
			{Title: "List facts", Args: map[string]any{"kind": "fact", "limit": 20}, Result: []map[string]any{{"id": 1, "kind": "fact", "content": "...", "updated_at": "2026-01-02T03:04:05Z"}}},
		},
		Tags: []string{"memory"},
	}
}

func (t MemoryList) Definition() ToolDef {
	spec := t.Spec()
	return ToolDef{Name: spec.Name, Description: tools.LLMDescription(spec), Parameters: spec.InputSchema}
}

func (t MemoryList) Execute(ctx context.Context, s *Session, rawArgs json.RawMessage) (any, error) {
	_ = ctx
	var args memoryListArgs
	_ = json.Unmarshal(rawArgs, &args)
	if s.DB == nil {
		return nil, errors.New("db not available")
	}
	kind := strings.TrimSpace(args.Kind)
	switch kind {
	case "", "fact", "pref", "task":
		// ok
	default:
		return nil, fmt.Errorf("invalid kind (expected fact|pref|task)")
	}
	limit := args.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > 200 {
		limit = 200
	}

	items, err := store.ListMemoryItems(s.DB, s.UserID, kind, limit)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(items))
	for _, it := range items {
		out = append(out, map[string]any{"id": it.ID, "kind": it.Kind, "content": it.Content, "updated_at": it.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z")})
	}
	return out, nil
}

type MemoryAdd struct{}

type memoryAddArgs struct {
	Kind    string `json:"kind"`
	Content string `json:"content"`
}

func (t MemoryAdd) Spec() tools.ToolSpec {
	return tools.ToolSpec{
		Name:    "memory.add",
		Summary: "Add a memory item.",
		Safety:  "Writes to the database (internal).",
		InputSchema: map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"kind":    map[string]any{"type": "string", "enum": []string{"fact", "pref", "task"}},
				"content": map[string]any{"type": "string", "minLength": 1},
			},
			"required": []string{"kind", "content"},
		},
		OutputSchema: map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"id": map[string]any{"type": "integer"},
			},
			"required": []string{"id"},
		},
		Examples: []tools.ToolExample{
			{Title: "Remember a preference", Args: map[string]any{"kind": "pref", "content": "Prefers concise answers."}, Result: map[string]any{"id": 123}},
		},
		Tags: []string{"memory"},
	}
}

func (t MemoryAdd) Definition() ToolDef {
	spec := t.Spec()
	return ToolDef{Name: spec.Name, Description: tools.LLMDescription(spec), Parameters: spec.InputSchema}
}

func (t MemoryAdd) Execute(ctx context.Context, s *Session, rawArgs json.RawMessage) (any, error) {
	_ = ctx
	var args memoryAddArgs
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return nil, err
	}
	if s.DB == nil {
		return nil, errors.New("db not available")
	}
	kind := strings.TrimSpace(args.Kind)
	if kind == "" {
		return nil, fmt.Errorf("kind required")
	}
	content := strings.TrimSpace(args.Content)
	if content == "" {
		return nil, fmt.Errorf("content required")
	}
	id, err := store.AddMemoryItem(s.DB, s.UserID, kind, content)
	if err != nil {
		return nil, err
	}
	return map[string]any{"id": id}, nil
}

type MemoryUpdate struct{}

type memoryUpdateArgs struct {
	ID      string `json:"id"`
	Content string `json:"content"`
}

func (t MemoryUpdate) Spec() tools.ToolSpec {
	return tools.ToolSpec{
		Name:    "memory.update",
		Summary: "Update a memory item by id.",
		Safety:  "Writes to the database (internal).",
		InputSchema: map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"id":      map[string]any{"type": "string", "minLength": 1, "description": "memory item id (stringified int)"},
				"content": map[string]any{"type": "string", "minLength": 1},
			},
			"required": []string{"id", "content"},
		},
		OutputSchema: map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"updated": map[string]any{"type": "integer"},
			},
			"required": []string{"updated"},
		},
		Examples: []tools.ToolExample{
			{Title: "Update a fact", Args: map[string]any{"id": "12", "content": "Lives in Berlin."}, Result: map[string]any{"updated": 12}},
		},
		Tags: []string{"memory"},
	}
}

func (t MemoryUpdate) Definition() ToolDef {
	spec := t.Spec()
	return ToolDef{Name: spec.Name, Description: tools.LLMDescription(spec), Parameters: spec.InputSchema}
}

func (t MemoryUpdate) Execute(ctx context.Context, s *Session, rawArgs json.RawMessage) (any, error) {
	_ = ctx
	var args memoryUpdateArgs
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return nil, err
	}
	if s.DB == nil {
		return nil, errors.New("db not available")
	}
	id, err := strconv.ParseInt(strings.TrimSpace(args.ID), 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid id")
	}
	if err := store.UpdateMemoryItem(s.DB, s.UserID, id, args.Content); err != nil {
		return nil, err
	}
	return map[string]any{"updated": id}, nil
}

type MemoryDelete struct{}

type memoryDeleteArgs struct {
	ID string `json:"id"`
}

func (t MemoryDelete) Spec() tools.ToolSpec {
	return tools.ToolSpec{
		Name:    "memory.delete",
		Summary: "Delete a memory item by id.",
		Safety:  "Destructive (deletes data). Consider confirming with the user first if unsure.",
		InputSchema: map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"id": map[string]any{"type": "string", "minLength": 1, "description": "memory item id (stringified int)"},
			},
			"required": []string{"id"},
		},
		OutputSchema: map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"deleted": map[string]any{"type": "integer"},
			},
			"required": []string{"deleted"},
		},
		Examples: []tools.ToolExample{
			{Title: "Delete an item", Args: map[string]any{"id": "12"}, Result: map[string]any{"deleted": 12}},
		},
		Tags: []string{"memory"},
	}
}

func (t MemoryDelete) Definition() ToolDef {
	spec := t.Spec()
	return ToolDef{Name: spec.Name, Description: tools.LLMDescription(spec), Parameters: spec.InputSchema}
}

func (t MemoryDelete) Execute(ctx context.Context, s *Session, rawArgs json.RawMessage) (any, error) {
	_ = ctx
	var args memoryDeleteArgs
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return nil, err
	}
	if s.DB == nil {
		return nil, errors.New("db not available")
	}
	id, err := strconv.ParseInt(strings.TrimSpace(args.ID), 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid id")
	}
	if err := store.DeleteMemoryItem(s.DB, s.UserID, id); err != nil {
		return nil, err
	}
	return map[string]any{"deleted": id}, nil
}
