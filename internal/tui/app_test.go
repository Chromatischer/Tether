package tui

import (
	"database/sql"
	"strings"
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
		chat:         newChatModel().withComposerContext("", false).withConversation(d, u.ID, newConv.ID),
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
		chat:         newChatModel().withComposerContext("", false).withConversation(d, u.ID, conv.ID),
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
			{Name: "search", Args: "{\"q\":\"tether\"}", Result: "{\n  \"results\": []\n}"},
		},
	})

	if len(updated.chat.messages) != 2 {
		t.Fatalf("expected streamed tool-call row plus assistant reply, got %d messages", len(updated.chat.messages))
	}
	if updated.chat.messages[0].role != "tool_call" {
		t.Fatalf("expected first row to remain the streamed tool call, got %+v", updated.chat.messages[0])
	}
	if !strings.Contains(updated.chat.messages[0].content, "\"results\": []") {
		t.Fatalf("expected streamed tool row to absorb result, got %+v", updated.chat.messages[0])
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
	if !strings.Contains(msgs[0].Content, "\"results\": []") {
		t.Fatalf("expected persisted tool_call content to include result, got %q", msgs[0].Content)
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
		chat:          newChatModel().withComposerContext("", false).withConversation(d, u.ID, conv.ID),
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
		t.Fatalf("expected one pending assistant indicator row, got %d", len(m.chat.messages))
	}
	if m.chat.messages[0].role != "assistant_pending" || !m.chat.messages[0].streaming {
		t.Fatalf("expected pending assistant indicator row, got %+v", m.chat.messages[0])
	}
}

func TestHandleCommandClearShowsFreshConversationMessageAsSystemNotice(t *testing.T) {
	d := openTUITestDB(t)
	u, err := store.CreateUser(d, "dana", "pw")
	if err != nil {
		t.Fatal(err)
	}
	conv, err := store.GetOrCreateDefaultConversation(d, u.ID)
	if err != nil {
		t.Fatal(err)
	}

	m := appModel{
		ctx:  &SessionContext{Config: &config.Config{}, DB: d},
		ag:   agent.New(&config.Config{}, d),
		user: u,
		conv: conv,
		chat: newChatModel().withComposerContext("", false).withConversation(d, u.ID, conv.ID),
	}

	updated, handled, _ := m.handleCommand("/clear")
	if !handled {
		t.Fatal("expected /clear to be handled")
	}
	if updated.conv == nil || updated.conv.ID == conv.ID {
		t.Fatal("expected /clear to activate a new conversation")
	}
	if len(updated.chat.messages) != 1 {
		t.Fatalf("expected one system notice in fresh chat, got %d messages", len(updated.chat.messages))
	}
	if updated.chat.messages[0].role != "system" {
		t.Fatalf("expected system notice, got %+v", updated.chat.messages[0])
	}
	if !strings.Contains(updated.chat.messages[0].content, "Started a fresh conversation with a clean agent context.") {
		t.Fatalf("unexpected notice content: %q", updated.chat.messages[0].content)
	}
}

func TestChatLoadCmdReloadsPersistedNoticeAsSystemNotice(t *testing.T) {
	d := openTUITestDB(t)
	u, err := store.CreateUser(d, "dana_reload", "pw")
	if err != nil {
		t.Fatal(err)
	}
	conv, err := store.GetOrCreateDefaultConversation(d, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AddMessage(d, conv.ID, "assistant", "usage: /resume <code>"); err != nil {
		t.Fatal(err)
	}

	m := newChatModel().withComposerContext("", false).withConversation(d, u.ID, conv.ID)
	msg := m.loadCmd()()
	loaded, ok := msg.(chatLoadedMsg)
	if !ok {
		t.Fatalf("expected chatLoadedMsg, got %T", msg)
	}
	if len(loaded.Messages) != 1 {
		t.Fatalf("expected 1 loaded message, got %d", len(loaded.Messages))
	}
	if loaded.Messages[0].role != "system" {
		t.Fatalf("expected persisted notice to reload as system, got %+v", loaded.Messages[0])
	}
}

func TestHandleCommandGroupedUsageIsMultiLine(t *testing.T) {
	d := openTUITestDB(t)
	u, err := store.CreateUser(d, "help_multiline", "pw")
	if err != nil {
		t.Fatal(err)
	}
	conv, err := store.GetOrCreateDefaultConversation(d, u.ID)
	if err != nil {
		t.Fatal(err)
	}

	m := appModel{
		ctx:  &SessionContext{Config: &config.Config{}, DB: d},
		user: u,
		conv: conv,
		chat: newChatModel().withComposerContext("", false).withConversation(d, u.ID, conv.ID),
	}

	updated, handled, _ := m.handleCommand("/tools nope")
	if !handled {
		t.Fatal("expected /tools nope to be handled")
	}
	if len(updated.chat.messages) == 0 {
		t.Fatal("expected usage message")
	}
	got := updated.chat.messages[len(updated.chat.messages)-1].content
	if !strings.Contains(got, "usage:\n  /tools list\n  /tools search <query>\n  /tools describe <name>") {
		t.Fatalf("expected multi-line usage block, got %q", got)
	}
}

func TestRenderHeaderStaysSingleLineWithActiveChatTab(t *testing.T) {
	m := appModel{
		w:    80,
		view: viewChat,
		user: &store.User{Username: "alice"},
	}

	header := m.renderHeader()
	if strings.Contains(header, "\n") {
		t.Fatalf("expected single-line header, got %q", header)
	}
}

func TestHitHeaderMatchesRenderedTabPositions(t *testing.T) {
	m := appModel{
		w:    80,
		view: viewChat,
		user: &store.User{Username: "alice"},
	}

	_, buttons, _, _ := m.headerLayout()
	if len(buttons) == 0 {
		t.Fatal("expected header buttons")
	}
	for _, button := range buttons {
		mid := button.X0 + (button.X1-button.X0)/2
		got, ok := m.hitHeader(mid)
		if !ok {
			t.Fatalf("expected hit for %q at x=%d", button.ID, mid)
		}
		if got.ID != button.ID {
			t.Fatalf("expected hit %q at x=%d, got %q", button.ID, mid, got.ID)
		}
	}
}

func TestChatPollNotificationsStoresSystemMessages(t *testing.T) {
	d := openTUITestDB(t)
	u, err := store.CreateUser(d, "erin", "pw")
	if err != nil {
		t.Fatal(err)
	}
	conv, err := store.GetOrCreateActiveConversation(d, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AddNotificationForConversation(d, u.ID, conv.ID, "self_schedule", "Follow up tomorrow."); err != nil {
		t.Fatal(err)
	}

	m := newChatModel().withConversation(d, u.ID, conv.ID)
	cmd := m.pollNotificationsCmd()
	if cmd == nil {
		t.Fatal("expected pollNotificationsCmd")
	}
	msg, ok := cmd().(chatNotificationsDeliveredMsg)
	if !ok {
		t.Fatalf("expected chatNotificationsDeliveredMsg, got %T", cmd())
	}
	if len(msg.Lines) != 1 || msg.Lines[0].role != "system" {
		t.Fatalf("expected one system line, got %+v", msg.Lines)
	}

	msgs, err := store.ListRecentMessages(d, conv.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) == 0 {
		t.Fatal("expected proactive notification to be delivered")
	}
	if msgs[0].Role != "system" {
		t.Fatalf("expected delivered proactive notification to be system role, got %+v", msgs[0])
	}
}

func TestBackendSyncLoadsMessagesWrittenOutsideTUI(t *testing.T) {
	d := openTUITestDB(t)
	u, err := store.CreateUser(d, "frank", "pw")
	if err != nil {
		t.Fatal(err)
	}
	conv, err := store.GetOrCreateActiveConversation(d, u.ID)
	if err != nil {
		t.Fatal(err)
	}

	m := appModel{
		ctx:  &SessionContext{Config: &config.Config{}, DB: d},
		user: u,
		conv: conv,
		chat: newChatModel().withComposerContext("", false).withConversation(d, u.ID, conv.ID),
	}
	loaded, ok := m.chat.loadCmd()().(chatLoadedMsg)
	if !ok {
		t.Fatal("expected initial chat load")
	}
	m.chat, _ = m.chat.Update(loaded)

	if err := store.AddMessage(d, conv.ID, "user", "discord says hi"); err != nil {
		t.Fatal(err)
	}
	if err := store.AddMessage(d, conv.ID, "assistant", "hello from discord"); err != nil {
		t.Fatal(err)
	}

	cmd := m.backendSyncNowCmd()
	if cmd == nil {
		t.Fatal("expected backend sync command")
	}
	msg, ok := cmd().(appBackendSyncMsg)
	if !ok {
		t.Fatalf("expected appBackendSyncMsg, got %T", cmd())
	}
	updatedModel, _ := m.Update(msg)
	updated := updatedModel.(appModel)

	if len(updated.chat.messages) != 2 {
		t.Fatalf("expected synced messages, got %+v", updated.chat.messages)
	}
	if updated.chat.messages[0].role != "user" || updated.chat.messages[0].content != "discord says hi" {
		t.Fatalf("unexpected first synced message: %+v", updated.chat.messages[0])
	}
	if updated.chat.messages[1].role != "assistant" || updated.chat.messages[1].content != "hello from discord" {
		t.Fatalf("unexpected second synced message: %+v", updated.chat.messages[1])
	}
}

func TestBackendSyncFollowsActiveConversationSwitch(t *testing.T) {
	d := openTUITestDB(t)
	u, err := store.CreateUser(d, "gina", "pw")
	if err != nil {
		t.Fatal(err)
	}
	convA, err := store.GetOrCreateActiveConversation(d, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	convB, err := store.CreateConversation(d, u.ID, "discord")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AddMessage(d, convA.ID, "assistant", "old chat"); err != nil {
		t.Fatal(err)
	}
	if err := store.AddMessage(d, convB.ID, "assistant", "new active chat"); err != nil {
		t.Fatal(err)
	}

	m := appModel{
		ctx:  &SessionContext{Config: &config.Config{}, DB: d},
		user: u,
		conv: convA,
		chat: newChatModel().withComposerContext("", false).withConversation(d, u.ID, convA.ID),
	}
	loaded, ok := m.chat.loadCmd()().(chatLoadedMsg)
	if !ok {
		t.Fatal("expected initial chat load")
	}
	m.chat, _ = m.chat.Update(loaded)

	if err := store.SetActiveConversation(d, u.ID, convB.ID); err != nil {
		t.Fatal(err)
	}

	cmd := m.backendSyncNowCmd()
	if cmd == nil {
		t.Fatal("expected backend sync command")
	}
	msg, ok := cmd().(appBackendSyncMsg)
	if !ok {
		t.Fatalf("expected appBackendSyncMsg, got %T", cmd())
	}
	updatedModel, _ := m.Update(msg)
	updated := updatedModel.(appModel)

	if updated.conv == nil || updated.conv.ID != convB.ID {
		t.Fatalf("expected synced active conversation %d, got %+v", convB.ID, updated.conv)
	}
	if len(updated.chat.messages) != 1 || updated.chat.messages[0].content != "new active chat" {
		t.Fatalf("expected reloaded new active conversation messages, got %+v", updated.chat.messages)
	}
}
