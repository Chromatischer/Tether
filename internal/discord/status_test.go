package discord

import (
	"strings"
	"testing"
	"time"

	"tether/internal/agent"
)

func TestRenderSessionStatusDiscord(t *testing.T) {
	got := renderSessionStatusDiscord(agent.SessionStatus{
		SessionID:         "sess-1",
		HasRuntimeSession: true,
		UsageSource:       "runtime",
		UserID:            7,
		ConversationID:    11,
		StartedAt:         time.Unix(100, 0).UTC(),
		LastActivityAt:    time.Unix(130, 0).UTC(),
		Age:               5 * time.Minute,
		Idle:              30 * time.Second,
		TotalToolCalls:    4,
		TotalInputTokens:  120,
		TotalOutputTokens: 30,
		TotalTokens:       150,
		TotalCost:         0.012345,
		LastModel:         "openai/gpt-4o",
		LastInputTokens:   64,
		LastContextLimit:  128000,
		LastContextPct:    0.05,
		AttachedContext: agent.AttachedContextStatus{
			Available:           true,
			PersonalityAttached: true,
			SummaryAttached:     true,
			HistoryMessages:     12,
			MemoryFacts:         3,
			MemoryPrefs:         2,
			MemoryTasks:         1,
			SkillsIndexAttached: true,
			InvokedSkills:       1,
			EstimatedTokens:     900,
			ContextLimit:        128000,
			ContextPct:          0.7,
		},
	})
	for _, want := range []string{"Session status", "runtime_session: active", "usage_source: runtime", "attached_context:", "history_messages: 12", "estimated_attached_tokens: 900 / 128000", "sess-1", "tool_calls: 4", "openai/gpt-4o", "last_request_context: 64 / 128000", "input_tokens: 120", "total_tokens: 150"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected %q in %q", want, got)
		}
	}
}
