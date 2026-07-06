package tui

import (
	"strings"
	"testing"
	"time"

	"tether/internal/config"
)

func TestRenderStatusBarShowsMetrics(t *testing.T) {
	m := newChatModel().withSize(120, 24)
	m = m.withSessionMetrics(sessionMetrics{
		hasData:     true,
		contextPct:  38,
		contextOK:   true,
		totalTokens: 2100,
		lastModel:   "openrouter/opus-4.8",
		activeRuns:  1,
	})
	out := m.renderStatusBar(120)
	for _, want := range []string{"ctx", "38%", "tok", "2.1k", "opus-4.8", "runs"} {
		if !strings.Contains(out, want) {
			t.Fatalf("status bar missing %q, got %q", want, out)
		}
	}
}

func TestRenderStatusBarWithoutDataStillShowsRuns(t *testing.T) {
	m := newChatModel().withSize(80, 24)
	out := m.renderStatusBar(80)
	if !strings.Contains(out, "runs") {
		t.Fatalf("expected runs segment with no metrics, got %q", out)
	}
	if strings.Contains(out, "ctx") {
		t.Fatalf("did not expect ctx gauge without data, got %q", out)
	}
}

func TestInspectorTogglesAndRendersToolDetail(t *testing.T) {
	m := newChatModel().withSize(140, 30)
	m = m.appendMessage(chatMessage{role: "tool_call", content: "bash  {\"command\":\"pwd\"}\n\n/work/output"})
	m = m.toggleInspector()
	if !m.inspectorOpen {
		t.Fatal("expected inspector to be open after toggle")
	}
	out := m.renderInspectorContent(60)
	for _, want := range []string{"INSPECTOR", "tool call", "bash", "/work/output"} {
		if !strings.Contains(out, want) {
			t.Fatalf("inspector missing %q, got %q", want, out)
		}
	}
	m = m.toggleInspector()
	if m.inspectorOpen {
		t.Fatal("expected inspector to close on second toggle")
	}
}

func TestInspectorErrorResultLabeled(t *testing.T) {
	m := newChatModel().withSize(140, 30)
	m = m.appendMessage(chatMessage{role: "tool_call", content: "web.fetch  {}\n\ntimeout after 5s"})
	m = m.toggleInspector()
	out := m.renderInspectorContent(60)
	if !strings.Contains(out, "result (error)") {
		t.Fatalf("expected error-labeled result, got %q", out)
	}
}

func TestToolCallResultUpdatesRunningRowDespiteArgDrift(t *testing.T) {
	m := newChatModel().withSize(120, 24)
	// Running event (no result) with one args representation.
	m = m.upsertStreamingToolCall(1, toolCallEntry{Name: "code", Args: `{"partial`})
	// Result event for the same call, but the args string differs (streamed /
	// truncated JSON). It must update the running row, not append a duplicate.
	m = m.upsertStreamingToolCall(1, toolCallEntry{Name: "code", Args: `{"code":"x"}`, Result: "/work/output"})

	count := 0
	var last chatMessage
	for _, msg := range m.messages {
		if msg.role == "tool_call" {
			count++
			last = msg
		}
	}
	if count != 1 {
		t.Fatalf("expected exactly 1 tool_call row, got %d", count)
	}
	parsed, _ := parseToolCallContent(last.content)
	if strings.TrimSpace(parsed.Result) == "" {
		t.Fatalf("expected the row to carry the result, got %q", last.content)
	}
	// The rendered row must not still claim it is running.
	if strings.Contains(formatMessage(last, 120, 0, TerminalProfile{}), "running") {
		t.Fatalf("expected completed row, still shows running: %q", last.content)
	}
}

func TestFinishStreamingCollapsesCompletedToolCall(t *testing.T) {
	m := newChatModel().withSize(120, 24)
	m = m.startStreamingAssistant(1)
	m = m.upsertStreamingToolCall(1, toolCallEntry{Name: "code", Args: "{}"})
	m = m.upsertStreamingToolCall(1, toolCallEntry{Name: "code", Args: "{}", Result: "done"})
	m = m.finishStreamingAssistant(1, "answer", "")

	found := false
	for _, msg := range m.messages {
		if msg.role != "tool_call" {
			continue
		}
		found = true
		if msg.streaming {
			t.Fatal("tool call should not be streaming after finish")
		}
		if msg.expanded {
			t.Fatal("completed tool call should collapse after finish")
		}
	}
	if !found {
		t.Fatal("expected a tool_call row to exist")
	}
}

func TestConnectorHealthOfflineWhenNotLive(t *testing.T) {
	cfg := &config.Config{}
	cfg.Signal.Enabled = true
	cfg.Discord.Enabled = true
	// live=false (terminal mode): connectors must report offline regardless of config.
	h := connectorHealthFor(nil, cfg, "openrouter/opus-4.8", false)
	if h.signal != connOff || h.discord != connOff || h.jobs != connOff {
		t.Fatalf("expected all connectors off in non-live mode, got %+v", h)
	}
	if h.model != "opus-4.8" {
		t.Fatalf("expected model to still resolve, got %q", h.model)
	}
}

func TestRenderConnectorClusterStates(t *testing.T) {
	out := renderConnectorCluster(connectorHealth{
		signal:  connOnline,
		discord: connOff,
		jobs:    connWarn,
		model:   "openrouter/opus-4.8",
	})
	for _, want := range []string{"sig", "dsc", "job", "opus-4.8"} {
		if !strings.Contains(out, want) {
			t.Fatalf("connector cluster missing %q, got %q", want, out)
		}
	}
}

func TestToolResultIsError(t *testing.T) {
	cases := map[string]bool{
		"timeout after 5s":  true,
		"error: nope":       true,
		`{"exit_code": 1}`:  true,
		`{"exit_code": 0}`:  false,
		"200 ok":            false,
		"3 events found":    false,
		"failed to connect": true,
	}
	for in, want := range cases {
		if got := toolResultIsError(in); got != want {
			t.Errorf("toolResultIsError(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestShortModelTrimsProvider(t *testing.T) {
	if got := shortModel("openrouter/anthropic/claude-opus-4.8"); got != "claude-opus-4.8" {
		t.Fatalf("shortModel = %q", got)
	}
	if got := shortModel("opus"); got != "opus" {
		t.Fatalf("shortModel passthrough = %q", got)
	}
}

func TestParseDBTime(t *testing.T) {
	got := parseDBTime("2026-06-17T09:14:05.000Z")
	if got.IsZero() {
		t.Fatal("expected parsed time, got zero")
	}
	if got.Hour() != 9 || got.Minute() != 14 {
		t.Fatalf("unexpected parse: %v", got)
	}
	if !parseDBTime("garbage").IsZero() {
		t.Fatal("expected zero time for unparseable input")
	}
}

func TestFormatMessageShowsTimestamp(t *testing.T) {
	ts := time.Date(2026, 6, 17, 9, 14, 0, 0, time.Local)
	out := formatMessage(chatMessage{role: "user", content: "hi", ts: ts}, 80, 0, TerminalProfile{})
	if !strings.Contains(out, "09:14") {
		t.Fatalf("expected timestamp in message, got %q", out)
	}
}
