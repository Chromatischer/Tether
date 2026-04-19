package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"tether/internal/agent/toolset"
	"tether/internal/llm/openrouter"
	"tether/internal/testutil"
	"tether/internal/tools"
)

type dummyTool struct {
	name string
}

func (d dummyTool) Spec() tools.ToolSpec {
	return tools.ToolSpec{
		Name:        d.name,
		Summary:     "d",
		InputSchema: map[string]any{"type": "object"},
		OutputSchema: map[string]any{
			"type": "object",
		},
		Examples: []tools.ToolExample{{Args: map[string]any{}}},
	}
}

func (d dummyTool) Definition() toolset.ToolDef {
	spec := d.Spec()
	return toolset.ToolDef{Name: spec.Name, Description: tools.LLMDescription(spec), Parameters: spec.InputSchema}
}

func (d dummyTool) Execute(ctx context.Context, s *toolset.Session, rawArgs json.RawMessage) (any, error) {
	return map[string]any{"args": string(rawArgs)}, nil
}

func TestActiveTools_SortedByName(t *testing.T) {
	ag := &Agent{toolImpl: map[string]toolset.Tool{"b": dummyTool{name: "b"}, "a": dummyTool{name: "a"}}}
	s := toolset.NewSession(tools.NewRegistry())
	s.Active = map[string]bool{"b": true, "a": true}
	nm := newToolNameMap(s.Registry)
	ts := ag.activeTools(s, nm)
	if len(ts) != 2 {
		t.Fatalf("expected 2 tools")
	}
	if ts[0].Name != "a" || ts[1].Name != "b" {
		t.Fatalf("expected sorted tools, got %+v", ts)
	}
}

func TestExecuteFunctionCalls_UnknownAndInactive(t *testing.T) {
	ag := &Agent{toolImpl: map[string]toolset.Tool{"known": dummyTool{name: "known"}}}
	s := toolset.NewSession(tools.NewRegistry())
	s.Active = map[string]bool{"known": false}

	nm := newToolNameMap(s.Registry)
	outs, _, _ := ag.executeFunctionCalls(context.Background(), s, []openrouter.ResponseItem{
		{Type: "function_call", CallID: "c1", Name: "unknown", Arguments: `{}`},
		{Type: "function_call", CallID: "c2", Name: "known", Arguments: `{}`},
	}, nm)
	if len(outs) != 2 {
		t.Fatalf("expected 2 outputs, got %d", len(outs))
	}
	if !strings.Contains(outs[0].Output, "unknown tool") {
		t.Fatalf("expected unknown tool error, got %q", outs[0].Output)
	}
	if !strings.Contains(outs[1].Output, "tool not enabled") {
		t.Fatalf("expected not enabled error, got %q", outs[1].Output)
	}
}

func TestExecuteFunctionCalls_AuditsArgsHashOnly(t *testing.T) {
	db := testutil.OpenTestDB(t)
	secretArgs := `{"x":"supersecret"}`
	sum := sha256.Sum256([]byte(secretArgs))
	expectedHash := hex.EncodeToString(sum[:])

	ag := &Agent{toolImpl: map[string]toolset.Tool{"t": dummyTool{name: "t"}}}
	s := toolset.NewSession(tools.NewRegistry())
	s.Active = map[string]bool{"t": true}
	s.DB = db
	s.UserID = 7

	nm := newToolNameMap(s.Registry)
	_, _, _ = ag.executeFunctionCalls(context.Background(), s, []openrouter.ResponseItem{{
		Type:      "function_call",
		CallID:    "call_123",
		Name:      "t",
		Arguments: secretArgs,
	}}, nm)

	var payload string
	if err := db.QueryRow(`SELECT payload_json FROM audit_events WHERE type='tool_call' ORDER BY id DESC LIMIT 1`).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(payload, "supersecret") {
		t.Fatalf("audit payload should not contain raw args: %s", payload)
	}
	if !strings.Contains(payload, expectedHash) {
		t.Fatalf("expected audit to contain args hash %s, got %s", expectedHash, payload)
	}
}

func TestTruncateAuditErr(t *testing.T) {
	long := strings.Repeat("x", 1000)
	got := truncateAuditErr(&testErr{s: long})
	max := 400 + len("…")
	if len(got) > max {
		t.Fatalf("expected truncated to <=%d bytes, got %d", max, len(got))
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("expected ellipsis")
	}
}

func TestReplayableResponseItems_FiltersReasoning(t *testing.T) {
	in := []openrouter.ResponseItem{
		{Type: "reasoning", ID: "rs_1"},
		{Type: "message", ID: "msg_1"},
		{Type: "function_call", ID: "fc_1"},
		{Type: "function_call_output", ID: "fco_1"},
	}
	got := replayableResponseItems(in)
	if len(got) != 2 {
		t.Fatalf("expected 2 replayable items, got %d", len(got))
	}
	if got[0].Type != "message" || got[1].Type != "function_call" {
		t.Fatalf("unexpected replayable items: %+v", got)
	}
}

func TestEmitReasoningSummaryDelta_AppendsOnlyNewSuffix(t *testing.T) {
	var b strings.Builder
	var deltas []string

	emitReasoningSummaryDelta(&b, "step 1", func(ev StreamEvent) {
		deltas = append(deltas, ev.Delta)
	})
	emitReasoningSummaryDelta(&b, "step 1\nstep 2", func(ev StreamEvent) {
		deltas = append(deltas, ev.Delta)
	})
	emitReasoningSummaryDelta(&b, "step 1\nstep 2", func(ev StreamEvent) {
		deltas = append(deltas, ev.Delta)
	})

	if b.String() != "step 1\nstep 2" {
		t.Fatalf("unexpected reasoning buffer %q", b.String())
	}
	if len(deltas) != 2 || deltas[0] != "step 1" || deltas[1] != "\nstep 2" {
		t.Fatalf("unexpected reasoning deltas: %#v", deltas)
	}
}

func TestNextJustificationThreshold(t *testing.T) {
	cases := []struct {
		total int
		want  int
	}{
		{total: -1, want: 25},
		{total: 0, want: 25},
		{total: 1, want: 25},
		{total: 24, want: 25},
		{total: 25, want: 50},
		{total: 26, want: 50},
		{total: 50, want: 75},
	}

	for _, tc := range cases {
		if got := nextJustificationThreshold(tc.total); got != tc.want {
			t.Fatalf("nextJustificationThreshold(%d) = %d, want %d", tc.total, got, tc.want)
		}
	}
}

func TestJustificationRequestItem(t *testing.T) {
	item := justificationRequestItem(25)
	if item.Type != "message" || item.Role != "system" {
		t.Fatalf("unexpected item envelope: %+v", item)
	}
	if len(item.Content) != 1 {
		t.Fatalf("expected single content part, got %+v", item.Content)
	}
	text := item.Content[0].Text
	if !strings.Contains(text, "25 tools") {
		t.Fatalf("expected tool count in prompt, got %q", text)
	}
	if !strings.Contains(text, "stop now and return to the user") {
		t.Fatalf("expected anti-loop instruction, got %q", text)
	}
	if !strings.Contains(text, "Do not call any tools in this response.") {
		t.Fatalf("expected no-tools instruction, got %q", text)
	}
}

type testErr struct{ s string }

func (e *testErr) Error() string { return e.s }
