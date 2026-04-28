package tui

import (
	"strings"
	"testing"
	"time"

	"tether/internal/agent"
	"tether/internal/store"
)

func TestRenderSessionStatusTUI(t *testing.T) {
	got := renderSessionStatusTUI(agent.SessionStatus{
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
			Available:             true,
			PersonalityAttached:   true,
			SummaryAttached:       true,
			HistoryMessages:       12,
			HistoryUserMessages:   6,
			HistoryAssistMessages: 6,
			MemoryFacts:           3,
			MemoryPrefs:           2,
			MemoryTasks:           1,
			SkillsIndexAttached:   true,
			InvokedSkills:         1,
			EstimatedTokens:       900,
			ContextLimit:          128000,
			ContextPct:            0.7,
		},
	})
	for _, want := range []string{"Session", "openai/gpt-4o", "tool calls", "Context window", "Usage", "$0.0123", "120 in", "30 out", "150 total", "Attached context", "12 msgs", "summary", "3 facts", "skills", "est. 900"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected %q in %q", want, got)
		}
	}
}

func TestHandleCommandStatus(t *testing.T) {
	d := openTUITestDB(t)
	u, err := store.CreateUser(d, "status-user", "pw")
	if err != nil {
		t.Fatal(err)
	}
	conv, err := store.GetOrCreateDefaultConversation(d, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	cfg := newTUITestConfig(t)
	ag := agent.New(cfg, d)
	m := appModel{
		ctx:  &SessionContext{Config: cfg, DB: d},
		user: u,
		conv: conv,
		ag:   ag,
		chat: newChatModel().withComposerContext("", false).withConversation(d, u.ID, conv.ID),
	}

	updated, handled, _ := m.handleCommand("/status")
	if !handled {
		t.Fatal("expected /status to be handled")
	}
	got := updated.chat.messages[len(updated.chat.messages)-1].content
	if !strings.Contains(got, "Session") {
		t.Fatalf("expected status output, got %q", got)
	}
	if !strings.Contains(got, "Context window") {
		t.Fatalf("expected context window in status output, got %q", got)
	}
}
