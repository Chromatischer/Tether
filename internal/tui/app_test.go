package tui

import (
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"

	"tether/internal/agent"
	"tether/internal/config"
	"tether/internal/db"
	"tether/internal/store"
)

func openTUITestDB(t *testing.T) *sql.DB {
	t.Helper()
	d, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(d); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d
}

func TestHandleAgentReplyStoresToOriginConversationAndDoesNotTouchActiveChat(t *testing.T) {
	d := openTUITestDB(t)
	u, err := store.CreateUser(d, "alice", "pw")
	if err != nil {
		t.Fatal(err)
	}
	oldConv, err := store.GetOrCreateDefaultConversation(d, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	newConv, err := store.CreateConversation(d, u.ID, "new")
	if err != nil {
		t.Fatal(err)
	}

	m := appModel{
		ctx:          &SessionContext{Config: &config.Config{}, DB: d},
		user:         u,
		conv:         newConv,
		chat:         newChatModel().withConversation(d, u.ID, newConv.ID),
		activeRuns:   1,
		releasedRuns: map[int]bool{41: false},
	}
	m.chat = m.chat.appendLocal("You", "current chat stays untouched")

	updated, _ := m.handleAgentReply(agentReplyMsg{
		ConversationID: oldConv.ID,
		RequestID:      41,
		Text:           "reply for old chat",
		ToolCalls: []toolCallEntry{
			{Name: "search", Args: "{\"q\":\"tether\"}"},
		},
	})

	if updated.activeRuns != 0 {
		t.Fatalf("expected activeRuns=0, got %d", updated.activeRuns)
	}
	if len(updated.chat.messages) != 1 || updated.chat.messages[0].role != "user" {
		t.Fatalf("expected active chat to remain unchanged, got %+v", updated.chat.messages)
	}

	msgs, err := store.ListRecentMessages(d, oldConv.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages in original conversation, got %d", len(msgs))
	}
	if msgs[0].Role != "tool_call" || msgs[1].Role != "assistant" {
		t.Fatalf("unexpected stored message roles: %+v", msgs)
	}
	if msgs[1].Content != "reply for old chat" {
		t.Fatalf("unexpected assistant content: %q", msgs[1].Content)
	}
}

func TestHandleAgentReplyDoesNotDuplicateStreamedToolCallsInActiveChat(t *testing.T) {
	d := openTUITestDB(t)
	u, err := store.CreateUser(d, "bob", "pw")
	if err != nil {
		t.Fatal(err)
	}
	conv, err := store.GetOrCreateDefaultConversation(d, u.ID)
	if err != nil {
		t.Fatal(err)
	}

	m := appModel{
		ctx:          &SessionContext{Config: &config.Config{}, DB: d},
		user:         u,
		conv:         conv,
		chat:         newChatModel().withConversation(d, u.ID, conv.ID),
		activeRuns:   1,
		releasedRuns: map[int]bool{9: false},
	}
	m.chat = m.chat.appendStreamingToolCall(9, "search", "{\"q\":\"tether\"}")
	m.chat = m.chat.startStreamingAssistant(9)

	updated, _ := m.handleAgentReply(agentReplyMsg{
		ConversationID: conv.ID,
		RequestID:      9,
		Text:           "done",
		ToolCalls: []toolCallEntry{
			{Name: "search", Args: "{\"q\":\"tether\"}"},
		},
	})

	if len(updated.chat.messages) != 2 {
		t.Fatalf("expected streamed tool-call row plus assistant reply, got %d messages", len(updated.chat.messages))
	}
	if updated.chat.messages[0].role != "tool_call" {
		t.Fatalf("expected first row to remain the streamed tool call, got %+v", updated.chat.messages[0])
	}
	if updated.chat.messages[1].role != "assistant" || updated.chat.messages[1].content != "done" {
		t.Fatalf("unexpected assistant message: %+v", updated.chat.messages[1])
	}

	msgs, err := store.ListRecentMessages(d, conv.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected persisted tool_call + assistant, got %d", len(msgs))
	}
}

func TestMaybeDispatchWaitlistStartsStreamingAssistantImmediately(t *testing.T) {
	d := openTUITestDB(t)
	u, err := store.CreateUser(d, "cara", "pw")
	if err != nil {
		t.Fatal(err)
	}
	conv, err := store.GetOrCreateDefaultConversation(d, u.ID)
	if err != nil {
		t.Fatal(err)
	}

	m := appModel{
		ctx:           &SessionContext{Config: &config.Config{}, DB: d},
		ag:            agent.New(&config.Config{}, d),
		user:          u,
		conv:          conv,
		chat:          newChatModel().withConversation(d, u.ID, conv.ID),
		nextRequestID: 1,
		releasedRuns:  map[int]bool{},
		waitlist:      []string{"test prompt"},
	}

	cmd := m.maybeDispatchWaitlist()
	if cmd == nil {
		t.Fatal("expected dispatch command")
	}
	if m.activeRuns != 1 {
		t.Fatalf("expected activeRuns=1, got %d", m.activeRuns)
	}
	if len(m.chat.messages) != 1 {
		t.Fatalf("expected streaming assistant placeholder, got %d messages", len(m.chat.messages))
	}
	if m.chat.messages[0].role != "assistant" || !m.chat.messages[0].streaming {
		t.Fatalf("expected streaming assistant placeholder, got %+v", m.chat.messages[0])
	}
	if m.chat.messages[0].content != "..." {
		t.Fatalf("expected placeholder content, got %q", m.chat.messages[0].content)
	}
}
