package tui

import (
	"strings"
	"testing"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
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

func TestFormatMessage_AssistantWithoutSeparateReasoningShowsExplicitNotice(t *testing.T) {
	out := formatMessage(chatMessage{role: "assistant", content: "Hi"}, 80)
	if !strings.Contains(out, "No separate model reasoning returned") {
		t.Fatalf("expected explicit no-reasoning notice, got %q", out)
	}
}

func TestFormatMessage_AssistantWithReasoningShowsModelReasoningLabel(t *testing.T) {
	out := formatMessage(chatMessage{role: "assistant", content: "Hi", reasoning: "step 1"}, 80)
	if !strings.Contains(out, "Model reasoning available") {
		t.Fatalf("expected reasoning affordance, got %q", out)
	}
}

func TestFormatMessage_SystemNoticeDoesNotRenderNoticeLabel(t *testing.T) {
	out := formatMessage(chatMessage{role: "system", content: "Started a fresh conversation."}, 80)
	if !strings.Contains(out, "Started a fresh conversation.") {
		t.Fatalf("expected notice content, got %q", out)
	}
	if strings.Contains(out, "notice") {
		t.Fatalf("expected no notice label, got %q", out)
	}
}

func TestFormatMessage_ToolCallShowsResultInSameWidget(t *testing.T) {
	out := formatMessage(chatMessage{role: "tool_call", content: "bash  {\"command\":\"pwd\"}\n\n{\n  \"exit_code\": 0,\n  \"stdout\": \"/work\\n\"\n}"}, 80)
	if !strings.Contains(out, "tool  bash") {
		t.Fatalf("expected tool label, got %q", out)
	}
	if !strings.Contains(out, "\"stdout\": \"/work") {
		t.Fatalf("expected result in same widget, got %q", out)
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

func TestStreamingReasoningStaysExpandedUntilAnswerTextStarts(t *testing.T) {
	m := newChatModel()

	m = m.setStreamingReasoning(3, "step 1")
	if len(m.messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(m.messages))
	}
	if !m.messages[0].reasoningExpanded {
		t.Fatal("expected streaming reasoning to start expanded before answer text")
	}

	m = m.setStreamingAssistant(3, "final answer")
	if m.messages[0].reasoningExpanded {
		t.Fatal("expected reasoning to collapse once answer text starts streaming")
	}
}

func TestFormatMessage_StreamingReasoningRendersAtTopOfBubble(t *testing.T) {
	out := formatMessage(chatMessage{role: "assistant", content: "...", reasoning: "step 1", streaming: true}, 80)
	if !strings.Contains(out, "Model reasoning") {
		t.Fatalf("expected streaming reasoning label, got %q", out)
	}
	if !strings.Contains(out, "step 1") {
		t.Fatalf("expected streaming reasoning text, got %q", out)
	}
}

func TestFormatMessage_StreamingReasoningHidesPlaceholderBody(t *testing.T) {
	out := formatMessage(chatMessage{role: "assistant", content: "...", reasoning: "step 1", streaming: true}, 80)
	if strings.Contains(out, "\n\n...") {
		t.Fatalf("expected placeholder body to be hidden while only reasoning is streaming, got %q", out)
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
