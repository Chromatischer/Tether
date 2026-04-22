package tui

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"tether/internal/agent"
	"tether/internal/config"
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
	for _, want := range []string{"```text", "Session status", "runtime_session: active", "usage_source: runtime", "Attached context", "history_messages: 12", "estimated_attached_tokens: 900 / 128000", "Recorded usage", "sess-1", "tool_calls: 4", "cost_usd: 0.012345", "openai/gpt-4o", "last_request_context: 64 / 128000", "context", "input", "output", "total", "```"} {
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
	ag := agent.New(&config.Config{}, d)
	m := appModel{
		ctx:  &SessionContext{Config: &config.Config{}, DB: d},
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
	if !strings.Contains(got, "Session status") {
		t.Fatalf("expected status output, got %q", got)
	}
	if !strings.Contains(got, "conversation_id: "+strconv.FormatInt(conv.ID, 10)) {
		t.Fatalf("expected conversation id in status output, got %q", got)
	}
}
