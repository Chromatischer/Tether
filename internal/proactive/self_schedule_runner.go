package proactive

import (
	"context"

	"tether/internal/store"
)

// SelfScheduleRunner executes a self-scheduled job with full agent capabilities (tool loop).
//
// This is implemented by the chat agent (internal/agent) and passed into the proactive
// scheduler to avoid an import cycle.
//
// Implementations should behave like a proactive/background run:
// - user is not present
// - output will be delivered later as a notification
// - avoid irreversible/external actions unless explicitly authorized
//
// activeTools is the snapshot of enabled tool names captured at scheduling time.
// Implementations should restrict tool access to exactly this set.
type SelfScheduleRunner interface {
	RunSelfSchedule(ctx context.Context, job store.SelfSchedule, activeTools []string) (string, error)
}
