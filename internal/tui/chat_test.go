package tui

import (
	"strings"
	"testing"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"

	"tether/internal/store"
)

func TestChatModelStreamingToolCallIsDeduplicated(t *testing.T) {
	m := newChatModel()

	m = m.appendStreamingToolCall(7, "search", "{\"q\":\"tether\"}")
	m = m.appendStreamingToolCall(7, "search", "{\"q\":\"tether\"}")

	if len(m.messages) != 1 {
		t.Fatalf("expected 1 tool-call row, got %d", len(m.messages))
	}
	if !m.hasStreamingToolCall(7, "search", "{\"q\":\"tether\"}") {
		t.Fatal("expected tool call to be tracked for the active stream")
	}

	m = m.startStreamingAssistant(7)
	m = m.finishStreamingAssistant(7, "done", "")

	if m.hasStreamingToolCall(7, "search", "{\"q\":\"tether\"}") {
		t.Fatal("expected tool-call tracking to be cleared after stream completion")
	}
}

func TestStreamingToolResultUpdatesExistingToolCallRow(t *testing.T) {
	m := newChatModel()

	m = m.appendStreamingToolCall(7, "bash", "{\"command\":\"pwd\"}")
	m = m.upsertStreamingToolCall(7, toolCallEntry{
		Name:   "bash",
		Args:   "{\"command\":\"pwd\"}",
		Result: "{\n  \"exit_code\": 0,\n  \"stdout\": \"/work\\n\"\n}",
	})

	if len(m.messages) != 1 {
		t.Fatalf("expected one merged tool row, got %d", len(m.messages))
	}
	if !strings.Contains(m.messages[0].content, "\"stdout\": \"/work") {
		t.Fatalf("expected tool result to be merged into tool row, got %q", m.messages[0].content)
	}
}

func TestAppendLocalMalformedToolCallFallsBackToSystemNotice(t *testing.T) {
	m := newChatModel()
	m = m.appendLocal("tool_call", "Here's what I've got at my disposal:")

	if len(m.messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(m.messages))
	}
	if m.messages[0].role != "system" {
		t.Fatalf("expected malformed tool content to fall back to system, got role=%q", m.messages[0].role)
	}
}

func TestAppendStreamingToolCallRejectsInvalidToolName(t *testing.T) {
	m := newChatModel()
	m = m.appendStreamingToolCall(12, "Here's what I've got", "")

	if len(m.messages) != 0 {
		t.Fatalf("expected invalid tool stream to be ignored, got %d messages", len(m.messages))
	}
	if m.hasStreamingToolCall(12, "Here's what I've got", "") {
		t.Fatal("expected invalid tool name to not be tracked")
	}
}

func TestFormatMessage_AssistantWithoutSeparateReasoningHasNoReasoningNotice(t *testing.T) {
	out := formatMessage(chatMessage{role: "assistant", content: "Hi"}, 80, 0, TerminalProfile{})
	if strings.Contains(out, "reasoning") {
		t.Fatalf("expected no reasoning notice for plain assistant message, got %q", out)
	}
	if !strings.Contains(out, "Hi") {
		t.Fatalf("expected assistant body text, got %q", out)
	}
}

func TestFormatMessage_AssistantDisablesGradientIn256ColorMode(t *testing.T) {
	out := formatMessage(chatMessage{role: "assistant", content: "Hi"}, 80, 0, TerminalProfile{DisableGradients: true})
	if strings.Contains(out, "38;2;") {
		t.Fatalf("expected no truecolor gradient escape codes in 256-color mode, got %q", out)
	}
}

func TestFormatMessage_AssistantReasoningShowsReasoningLabel(t *testing.T) {
	out := formatMessage(chatMessage{role: "assistant_reasoning", content: "step 1"}, 80, 0, TerminalProfile{})
	if !strings.Contains(out, "◈ reasoning") {
		t.Fatalf("expected reasoning affordance, got %q", out)
	}
}

func TestFormatMessage_SystemNoticeDoesNotRenderNoticeLabel(t *testing.T) {
	out := formatMessage(chatMessage{role: "system", content: "Started a fresh conversation."}, 80, 0, TerminalProfile{})
	if !strings.Contains(out, "Started a fresh conversation.") {
		t.Fatalf("expected notice content, got %q", out)
	}
	if strings.Contains(out, "notice") {
		t.Fatalf("expected no notice label, got %q", out)
	}
}

func TestFormatMessage_ToolCallShowsResultInSameWidget(t *testing.T) {
	out := formatMessage(chatMessage{role: "tool_call", content: "bash  {\"command\":\"pwd\"}\n\n{\n  \"exit_code\": 0,\n  \"stdout\": \"/work\\n\"\n}", expanded: true}, 80, 0, TerminalProfile{})
	if !strings.Contains(out, "▷") {
		t.Fatalf("expected tool icon ▷, got %q", out)
	}
	if !strings.Contains(out, "bash") {
		t.Fatalf("expected tool name bash, got %q", out)
	}
	if !strings.Contains(out, "\"stdout\": \"/work") {
		t.Fatalf("expected result content, got %q", out)
	}
}

func TestRenderRichText_StrongTextUsesAnsiStyling(t *testing.T) {
	out := renderRichText("plain **bold** text", 40, richTextAssistant)
	if !strings.Contains(out, "bold") {
		t.Fatalf("expected bold text content, got %q", out)
	}
	if !strings.Contains(out, "\x1b[") {
		t.Fatalf("expected ANSI styling, got %q", out)
	}
}

func TestRenderRichText_HorizontalRuleExpandsToLine(t *testing.T) {
	out := stripANSI(renderRichText("---", 24, richTextAssistant))
	if !strings.Contains(out, strings.Repeat("─", 22)) {
		t.Fatalf("expected rendered horizontal rule, got %q", out)
	}
}

func TestRenderRichText_TableWrapsWithinWidth(t *testing.T) {
	src := "| Column A | Column B |\n| --- | --- |\n| very long cell value | another long cell value |"
	out := stripANSI(renderRichText(src, 24, richTextAssistant))
	for _, line := range strings.Split(out, "\n") {
		if utf8.RuneCountInString(line) > 24 {
			t.Fatalf("expected wrapped table line width <= 24, got %d in %q", utf8.RuneCountInString(line), line)
		}
	}
	if !strings.Contains(out, "Column A") || !strings.Contains(out, "another") {
		t.Fatalf("expected table content, got %q", out)
	}
}

func TestStreamingReasoningAndAnswerRenderAsSeparateTimelineRows(t *testing.T) {
	m := newChatModel()

	m = m.setStreamingReasoning(3, "step 1")
	if len(m.messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(m.messages))
	}
	if m.messages[0].role != "assistant_reasoning" {
		t.Fatalf("expected first message to be reasoning, got %+v", m.messages[0])
	}

	m = m.setStreamingAssistant(3, "final answer")
	if len(m.messages) != 2 {
		t.Fatalf("expected separate reasoning and answer rows, got %d", len(m.messages))
	}
	if m.messages[1].role != "assistant" || m.messages[1].content != "final answer" {
		t.Fatalf("unexpected answer row: %+v", m.messages[1])
	}
}

func TestFormatMessage_StreamingReasoningRendersAsOwnRow(t *testing.T) {
	out := formatMessage(chatMessage{role: "assistant_reasoning", content: "step 1", streaming: true}, 80, 0, TerminalProfile{})
	if !strings.Contains(out, "◈ reasoning") {
		t.Fatalf("expected streaming reasoning label, got %q", out)
	}
	if !strings.Contains(out, "step 1") {
		t.Fatalf("expected streaming reasoning text, got %q", out)
	}
}

func TestStreamingReasoningResumesAsNewTimelineSegmentAfterToolCall(t *testing.T) {
	m := newChatModel()
	m = m.setStreamingReasoning(3, "step 1")
	m = m.appendStreamingToolCall(3, "search", "{\"q\":\"x\"}")
	m = m.setStreamingReasoning(3, "step 1step 2")

	if len(m.messages) != 3 {
		t.Fatalf("expected reasoning, tool, reasoning rows; got %d", len(m.messages))
	}
	if m.messages[2].role != "assistant_reasoning" || m.messages[2].content != "step 2" {
		t.Fatalf("expected second reasoning segment to contain only the new suffix, got %+v", m.messages[2])
	}
}

func TestStreamingTimelinePersistsForReload(t *testing.T) {
	d := openTUITestDB(t)
	u, err := store.CreateUser(d, "timeline_persist", "pw")
	if err != nil {
		t.Fatal(err)
	}
	conv, err := store.GetOrCreateDefaultConversation(d, u.ID)
	if err != nil {
		t.Fatal(err)
	}

	m := newChatModel().withConversation(d, u.ID, conv.ID)
	m = m.setStreamingReasoning(7, "reason 1")
	m = m.appendStreamingToolCall(7, "search", "{\"q\":\"tether\"}")
	m = m.upsertStreamingToolCall(7, toolCallEntry{Name: "search", Args: "{\"q\":\"tether\"}", Result: "{\"items\":[]}"})
	m = m.setStreamingAssistant(7, "answer 1")
	m = m.finishStreamingAssistant(7, "answer 1", "reason 1")

	msgs, err := store.ListRecentMessages(d, conv.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 3 {
		t.Fatalf("expected reasoning, tool, answer rows; got %d", len(msgs))
	}
	if msgs[0].Role != "assistant_reasoning" || msgs[1].Role != "tool_call" || msgs[2].Role != "assistant" {
		t.Fatalf("unexpected persisted roles: %+v", msgs)
	}
	if msgs[0].Content != "reason 1" || !strings.Contains(msgs[1].Content, "\"items\":[]") || msgs[2].Content != "answer 1" {
		t.Fatalf("unexpected persisted contents: %+v", msgs)
	}
}

func TestFinishStreamingAssistantCollapsesReasoningAndToolRows(t *testing.T) {
	m := newChatModel()
	m = m.setStreamingReasoning(9, "reason")
	m = m.appendStreamingToolCall(9, "search", "{\"q\":\"tether\"}")
	m = m.upsertStreamingToolCall(9, toolCallEntry{Name: "search", Args: "{\"q\":\"tether\"}", Result: "{\"items\":[]}"})
	m = m.setStreamingAssistant(9, "answer")
	m = m.finishStreamingAssistant(9, "answer", "reason")

	if m.messages[0].role != "assistant_reasoning" || m.messages[0].expanded {
		t.Fatalf("expected completed reasoning row to be collapsed, got %+v", m.messages[0])
	}
	if m.messages[1].role != "tool_call" || m.messages[1].expanded {
		t.Fatalf("expected completed tool row to be collapsed, got %+v", m.messages[1])
	}
}

func TestFormatMessage_CollapsedToolCallShowsSummaryNotRunning(t *testing.T) {
	out := formatMessage(chatMessage{role: "tool_call", content: "bash  {\"command\":\"pwd\"}\n\n{\"exit_code\":0}", expanded: false}, 80, 0, TerminalProfile{})
	if strings.Contains(out, "running") {
		t.Fatalf("expected completed tool row to avoid running state, got %q", out)
	}
	if !strings.Contains(out, "click to expand") {
		t.Fatalf("expected collapsed tool summary hint, got %q", out)
	}
}

func TestToggleExpandableAtExpandsClickedCompletedReasoningRow(t *testing.T) {
	m := newChatModel().withSize(80, 24)
	m = m.appendMessage(chatMessage{role: "assistant_reasoning", content: "reason"})
	m.messages[0].expanded = false
	m.reflow()
	if len(m.rowHits) != 1 {
		t.Fatalf("expected one row hit, got %d", len(m.rowHits))
	}
	y := 1 + m.rowHits[0].startLine
	m = m.toggleExpandableAt(y)
	if !m.messages[0].expanded {
		t.Fatalf("expected clicked reasoning row to expand, got %+v", m.messages[0])
	}
}

func TestMatchCommandSuggestionsFiltersByPrefix(t *testing.T) {
	got := matchCommandSuggestions("/too", false)
	if len(got) == 0 {
		t.Fatal("expected command suggestions")
	}
	if got[0].Label != "/tools list" {
		t.Fatalf("expected /tools list first, got %q", got[0].Label)
	}
	for _, item := range got {
		if !strings.HasPrefix(item.Label, "/too") {
			t.Fatalf("unexpected suggestion %q for /too", item.Label)
		}
	}
}

func TestMatchSkillSuggestionsFiltersByPrefix(t *testing.T) {
	got := matchSkillSuggestions("$git", []string{"github", "frontend-skill", "gitops"})
	if len(got) != 2 {
		t.Fatalf("expected 2 skill suggestions, got %d", len(got))
	}
	if got[0].Label != "$github" || got[1].Label != "$gitops" {
		t.Fatalf("unexpected skill suggestions: %+v", got)
	}
}

func TestChatModelTabCyclesSuggestionsThenSend(t *testing.T) {
	m := newChatModel()
	m.textarea.SetValue("/to")
	m.syncAutocomplete()

	var handled bool
	m, handled = m.handleTab(false)
	if !handled || m.focus != composerFocusSuggestion || m.selectedSuggestion != 0 {
		t.Fatalf("expected first tab to focus first suggestion, got focus=%v selected=%d", m.focus, m.selectedSuggestion)
	}

	for i := 1; i < len(m.suggestions); i++ {
		m, handled = m.handleTab(false)
		if !handled {
			t.Fatal("expected tab navigation to stay handled")
		}
	}
	if m.focus != composerFocusSuggestion || m.selectedSuggestion != len(m.suggestions)-1 {
		t.Fatalf("expected last suggestion selected, got focus=%v selected=%d", m.focus, m.selectedSuggestion)
	}

	m, handled = m.handleTab(false)
	if !handled || m.focus != composerFocusSend {
		t.Fatalf("expected tab after suggestions to focus Send, got focus=%v", m.focus)
	}
}

func TestChatModelEnterAppliesSuggestionAndKeepsEditing(t *testing.T) {
	m := newChatModel()
	m.textarea.SetValue("/mem")
	m.syncAutocomplete()
	m.focus = composerFocusSuggestion
	m.selectedSuggestion = 1

	updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd != nil {
		t.Fatal("expected no send command when applying suggestion")
	}
	if !strings.HasPrefix(updated.textarea.Value(), "/memory ") {
		t.Fatalf("expected applied /memory suggestion, got %q", updated.textarea.Value())
	}
	if updated.focus != composerFocusInput {
		t.Fatalf("expected focus to return to input, got %v", updated.focus)
	}
	if len(updated.suggestions) != 0 {
		t.Fatalf("expected suggestions to stay dismissed after apply, got %d", len(updated.suggestions))
	}
	if !updated.autocompleteDismissed {
		t.Fatal("expected autocomplete to remain dismissed until the user edits again")
	}
	updated, handled := updated.handleTab(false)
	if !handled || updated.focus != composerFocusSend {
		t.Fatalf("expected next tab after apply to reach Send, got focus=%v", updated.focus)
	}
}

func TestChatModelEnterOnSendDispatchesMessage(t *testing.T) {
	m := newChatModel()
	m.textarea.SetValue("hello")
	m.focus = composerFocusSend

	updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected send command")
	}
	msg := cmd()
	send, ok := msg.(chatSendMsg)
	if !ok {
		t.Fatalf("expected chatSendMsg, got %T", msg)
	}
	if send.Text != "hello" {
		t.Fatalf("unexpected sent text %q", send.Text)
	}
	if updated.textarea.Value() != "" {
		t.Fatalf("expected textarea reset after send, got %q", updated.textarea.Value())
	}
}

func TestFormatMessage_UserMessageHasSenderGlyph(t *testing.T) {
	out := formatMessage(chatMessage{role: "user", content: "hello"}, 80, 0, TerminalProfile{})
	if !strings.Contains(out, "you ›") {
		t.Fatalf("expected sender glyph 'you ›', got %q", out)
	}
	if !strings.Contains(out, "hello") {
		t.Fatalf("expected message content, got %q", out)
	}
}

func TestFormatMessage_SystemErrorPrefixGetsErrorStyle(t *testing.T) {
	out := formatMessage(chatMessage{role: "system", content: "failed to load messages: db error"}, 80, 0, TerminalProfile{})
	if !strings.Contains(out, "✗ error") {
		t.Fatalf("expected error sender label, got %q", out)
	}
}

func TestFormatMessage_SystemSuccessPrefixGetsInfoStyle(t *testing.T) {
	out := formatMessage(chatMessage{role: "system", content: "memory added (id 7)"}, 80, 0, TerminalProfile{})
	if !strings.Contains(out, "✓ info") {
		t.Fatalf("expected info sender label, got %q", out)
	}
}

func TestFormatMessage_ProactiveNoticeUsesSystemStyle(t *testing.T) {
	out := formatMessage(chatMessage{role: "system", content: "[Proactive/self_schedule] Follow up tomorrow."}, 80, 0, TerminalProfile{})
	if !strings.Contains(out, "● system") {
		t.Fatalf("expected system sender label, got %q", out)
	}
	if strings.Contains(out, "◆ Tether") {
		t.Fatalf("expected proactive notice to avoid assistant styling, got %q", out)
	}
}

func TestNewChatModelComposerDefaultsToThreeRows(t *testing.T) {
	m := newChatModel().withSize(80, 24)
	if got := m.composerHeight(80); got != 4 {
		t.Fatalf("expected 4-row composer, got %d", got)
	}
}

func TestNewChatModelTextareaDoesNotRenderInternalPrompt(t *testing.T) {
	m := newChatModel()
	if m.textarea.Prompt != "" {
		t.Fatalf("expected textarea prompt to be empty, got %q", m.textarea.Prompt)
	}
}

func TestFormatMessage_ToolCallInProgressShowsRunning(t *testing.T) {
	out := formatMessage(chatMessage{role: "tool_call", content: "bash  pwd"}, 80, 0, TerminalProfile{})
	if !strings.Contains(out, "▷") {
		t.Fatalf("expected tool icon, got %q", out)
	}
	if !strings.Contains(out, "running") {
		t.Fatalf("expected in-progress indicator, got %q", out)
	}
}

func TestStreamTickAdvancesFrameAndReflows(t *testing.T) {
	m := newChatModel()
	m = m.startStreamingAssistant(1)

	if !m.hasStreamingMessages() {
		t.Fatal("expected streaming message")
	}

	frame0 := m.streamFrame
	updated, cmd := m.Update(streamTickMsg{})
	if updated.streamFrame != frame0+1 {
		t.Fatalf("expected streamFrame to advance, got %d → %d", frame0, updated.streamFrame)
	}
	if cmd == nil {
		t.Fatal("expected ticker to re-fire while streaming")
	}
}

func TestPendingAssistantIndicatorClearsOnFirstRealStreamEvent(t *testing.T) {
	m := newChatModel()
	m = m.startStreamingAssistant(1)
	if len(m.messages) != 1 || m.messages[0].role != "assistant_pending" {
		t.Fatalf("expected pending assistant row, got %+v", m.messages)
	}
	m = m.setStreamingReasoning(1, "step 1")
	if len(m.messages) != 2 {
		t.Fatalf("expected reasoning row plus trailing pending indicator, got %+v", m.messages)
	}
	if m.messages[0].role != "assistant_reasoning" || m.messages[1].role != "assistant_pending" {
		t.Fatalf("expected pending row to remain latest after first real stream event, got %+v", m.messages)
	}
}

func TestPendingAssistantIndicatorStaysLatestDuringStreamingTurn(t *testing.T) {
	m := newChatModel()
	m = m.startStreamingAssistant(1)
	m = m.setStreamingReasoning(1, "step 1")
	m = m.appendStreamingToolCall(1, "search", "{\"q\":\"x\"}")
	m = m.setStreamingAssistant(1, "answer")

	if got := m.messages[len(m.messages)-1].role; got != "assistant_pending" {
		t.Fatalf("expected pending indicator to stay latest while turn is active, got %q", got)
	}
}

func TestStreamTickStopsAfterStreamingEnds(t *testing.T) {
	m := newChatModel()
	m = m.startStreamingAssistant(1)
	m = m.finishStreamingAssistant(1, "done", "")

	updated, cmd := m.Update(streamTickMsg{})
	_ = updated
	if cmd != nil {
		t.Fatal("expected ticker to stop after streaming finished")
	}
}
