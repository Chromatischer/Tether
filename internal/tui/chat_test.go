package tui

import (
	"strings"
	"testing"
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
