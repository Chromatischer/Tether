package toolset

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"tether/internal/store"
)

type MemoryList struct{}

type memoryListArgs struct {
	Kind  string `json:"kind"`
	Limit int    `json:"limit"`
}

func (t MemoryList) Definition() ToolDef {
	return ToolDef{
		Name:        "memory.list",
		Description: "List memory items for the current user. kind can be fact|pref|task (or empty for all).",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"kind":  map[string]any{"type": "string"},
				"limit": map[string]any{"type": "integer"},
			},
		},
	}
}

func (t MemoryList) Execute(ctx context.Context, s *Session, rawArgs json.RawMessage) (any, error) {
	_ = ctx
	var args memoryListArgs
	_ = json.Unmarshal(rawArgs, &args)
	if s.DB == nil {
		return nil, errors.New("db not available")
	}
	items, err := store.ListMemoryItems(s.DB, s.UserID, strings.TrimSpace(args.Kind), args.Limit)
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

func (t MemoryAdd) Definition() ToolDef {
	return ToolDef{
		Name:        "memory.add",
		Description: "Add a memory item for the current user.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"kind":    map[string]any{"type": "string"},
				"content": map[string]any{"type": "string"},
			},
			"required": []string{"kind", "content"},
		},
	}
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

func (t MemoryUpdate) Definition() ToolDef {
	return ToolDef{
		Name:        "memory.update",
		Description: "Update the content of an existing memory item by id.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id":      map[string]any{"type": "string"},
				"content": map[string]any{"type": "string"},
			},
			"required": []string{"id", "content"},
		},
	}
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

func (t MemoryDelete) Definition() ToolDef {
	return ToolDef{
		Name:        "memory.delete",
		Description: "Delete a memory item by id.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id": map[string]any{"type": "string"},
			},
			"required": []string{"id"},
		},
	}
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
