package proactive

import (
	"context"

	"tether/internal/store"
)

// SelfScheduleRunner executes a fired self-schedule as a "wakeup": it continues
// the conversation from where it left off, using the live session and the normal
// chat system prompt, and injects the scheduled prompt as the next turn.
//
// This is implemented by the chat agent (internal/agent) and passed into the
// proactive scheduler to avoid an import cycle.
type SelfScheduleRunner interface {
	RunSelfSchedule(ctx context.Context, job store.SelfSchedule) (string, error)
}
