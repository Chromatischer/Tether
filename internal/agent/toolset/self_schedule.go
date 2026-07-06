package toolset

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"tether/internal/store"
	"tether/internal/tools"
)

type SelfSchedule struct{}

type selfScheduleArgs struct {
	Action       string `json:"action"`        // create | list | cancel
	Prompt       string `json:"prompt"`        // create
	RunAt        string `json:"run_at"`        // RFC3339, create
	DelaySeconds int64  `json:"delay_seconds"` // create
	Delay        string `json:"delay"`         // create; Go duration string like "10m" or "2h"
	ID           int64  `json:"id"`            // cancel
	Limit        int    `json:"limit"`         // list
}

func (t SelfSchedule) Spec() tools.ToolSpec {
	return tools.ToolSpec{
		Name:     "self.schedule",
		Category: tools.CategoryScheduling,
		Summary:  "Schedule a future 'wakeup' that continues this conversation later (one-off).",
		WhenToUse: "Use this to follow up later without user input (reminders, check-ins, delayed work). " +
			"When it fires, the agent simply continues THIS conversation from wherever it is at that moment: current history, the normal system prompt, and the live tool set. " +
			"It is NOT a snapshot — it will see messages added after you schedule it, and use whatever tools are enabled when it fires. " +
			"The scheduled `prompt` is injected as the next turn (as if it had just been sent). " +
			"Timing note: schedules are polled on a ~1 minute tick, so firing can have up to ~1 minute of jitter.",
		Safety: "Creates an autonomous background run that continues the conversation later. " +
			"Not allowed from sub-agents.",
		InputSchema: map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"action":        map[string]any{"type": "string", "description": "create (default), list, cancel"},
				"prompt":        map[string]any{"type": "string", "description": "What the agent should do/say when the schedule fires."},
				"run_at":        map[string]any{"type": "string", "description": "When to run (RFC3339 timestamp, e.g. 2026-04-17T20:15:00Z)."},
				"delay_seconds": map[string]any{"type": "integer", "minimum": 1, "description": "Run after this many seconds from now."},
				"delay":         map[string]any{"type": "string", "description": "Run after this duration from now (Go duration like 10m, 2h, 30s)."},
				"id":            map[string]any{"type": "integer", "minimum": 1, "description": "Schedule id (for cancel)."},
				"limit":         map[string]any{"type": "integer", "minimum": 1, "maximum": 50, "description": "List limit (for list)."},
			},
		},
		OutputSchema: map[string]any{
			"oneOf": []any{
				map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"id":     map[string]any{"type": "integer"},
						"run_at": map[string]any{"type": "string"},
					},
					"required": []string{"id", "run_at"},
				},
				map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"schedules": map[string]any{"type": "array"},
					},
					"required": []string{"schedules"},
				},
				map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"canceled": map[string]any{"type": "boolean"},
					},
					"required": []string{"canceled"},
				},
			},
		},
		Examples: []tools.ToolExample{
			{Title: "Schedule a follow-up in 10 minutes", Args: map[string]any{"prompt": "Check back and ask if the task is complete.", "delay": "10m"}, Result: map[string]any{"id": 123, "run_at": "2026-04-17T20:15:00Z"}},
			{Title: "Schedule at a specific time", Args: map[string]any{"prompt": "Send a reminder.", "run_at": "2026-04-17T20:15:00Z"}, Result: map[string]any{"id": 124, "run_at": "2026-04-17T20:15:00Z"}},
		},
		Tags: []string{"proactive", "scheduler"},
	}
}

func (t SelfSchedule) Definition() ToolDef {
	spec := t.Spec()
	return ToolDef{Name: spec.Name, Description: tools.LLMDescription(spec), Parameters: spec.InputSchema}
}

func (t SelfSchedule) Execute(ctx context.Context, s *Session, rawArgs json.RawMessage) (any, error) {
	_ = ctx
	if s == nil {
		return nil, errors.New("session not available")
	}
	if s.IsSubagent {
		return nil, errors.New("self.schedule is not allowed in sub-agent context")
	}
	if s.DB == nil {
		return nil, errors.New("db not available")
	}
	if s.UserID == 0 {
		return nil, errors.New("user not available")
	}
	if s.ConversationID == 0 {
		return nil, errors.New("conversation not available")
	}

	var args selfScheduleArgs
	if len(rawArgs) > 0 {
		if err := json.Unmarshal(rawArgs, &args); err != nil {
			return nil, err
		}
	}
	action := strings.TrimSpace(strings.ToLower(args.Action))
	if action == "" {
		action = "create"
	}

	switch action {
	case "list":
		limit := args.Limit
		if limit <= 0 {
			limit = 20
		}
		items, err := store.ListPendingSelfSchedules(s.DB, s.UserID, s.ConversationID, limit)
		if err != nil {
			return nil, err
		}
		// Keep the payload compact.
		out := make([]map[string]any, 0, len(items))
		for _, it := range items {
			p := strings.TrimSpace(it.Prompt)
			if len(p) > 200 {
				p = p[:200] + "…"
			}
			out = append(out, map[string]any{
				"id":                it.ID,
				"run_at":            it.RunAt.Format(time.RFC3339),
				"anchor_message_id": it.AnchorMessageID,
				"prompt":            p,
			})
		}
		return map[string]any{"schedules": out}, nil

	case "cancel":
		if args.ID <= 0 {
			return nil, errors.New("id required")
		}
		ok, err := store.CancelSelfSchedule(s.DB, s.UserID, s.ConversationID, args.ID)
		if err != nil {
			return nil, err
		}
		return map[string]any{"canceled": ok}, nil

	case "create":
		prompt := strings.TrimSpace(args.Prompt)
		if prompt == "" {
			return nil, errors.New("prompt required")
		}

		now := time.Now().UTC()
		runAt, err := parseSelfScheduleRunAt(now, args)
		if err != nil {
			return nil, err
		}
		if runAt.Before(now.Add(2 * time.Second)) {
			runAt = now.Add(2 * time.Second)
		}

		// A fired schedule continues the live conversation as a wakeup, so there is
		// no anchor snapshot and no tool snapshot — it uses whatever state the chat
		// is in when it fires.
		id, err := store.CreateSelfSchedule(s.DB, s.UserID, s.ConversationID, 0, prompt, runAt, "[]")
		if err != nil {
			return nil, err
		}

		// Best-effort audit (don’t store prompt content).
		uid := s.UserID
		payload, _ := json.Marshal(map[string]any{
			"schedule_id":     id,
			"conversation_id": s.ConversationID,
			"run_at":          runAt.Format(time.RFC3339),
			"prompt_len":      len(prompt),
		})
		_ = store.AddAuditEvent(s.DB, &uid, "self_schedule_create", string(payload))

		return map[string]any{"id": id, "run_at": runAt.Format(time.RFC3339)}, nil

	default:
		return nil, fmt.Errorf("invalid action: %s", action)
	}
}

func parseSelfScheduleRunAt(now time.Time, args selfScheduleArgs) (time.Time, error) {
	if args.DelaySeconds > 0 {
		return now.Add(time.Duration(args.DelaySeconds) * time.Second), nil
	}
	if strings.TrimSpace(args.Delay) != "" {
		d, err := time.ParseDuration(strings.TrimSpace(args.Delay))
		if err != nil {
			return time.Time{}, fmt.Errorf("invalid delay: %w", err)
		}
		if d <= 0 {
			return time.Time{}, errors.New("delay must be > 0")
		}
		return now.Add(d), nil
	}
	if strings.TrimSpace(args.RunAt) != "" {
		s := strings.TrimSpace(args.RunAt)
		// RFC3339 is the primary format.
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			return t.UTC(), nil
		}
		// Friendlier fallback: "YYYY-MM-DD HH:MM" interpreted as UTC.
		if t, err := time.ParseInLocation("2006-01-02 15:04", s, time.UTC); err == nil {
			return t.UTC(), nil
		}
		return time.Time{}, errors.New("run_at must be RFC3339 (e.g. 2026-04-17T20:15:00Z)")
	}
	return time.Time{}, errors.New("specify one of: run_at, delay_seconds, delay")
}
