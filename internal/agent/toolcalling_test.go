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
	tools := ag.activeTools(s)
	if len(tools) != 2 {
		t.Fatalf("expected 2 tools")
	}
	if tools[0].Function.Name != "a" || tools[1].Function.Name != "b" {
		t.Fatalf("expected sorted tools, got %+v", tools)
	}
}

func TestExecuteToolCalls_UnknownAndInactive(t *testing.T) {
	ag := &Agent{toolImpl: map[string]toolset.Tool{"known": dummyTool{name: "known"}}}
	s := toolset.NewSession(tools.NewRegistry())
	s.Active = map[string]bool{"known": false}

	msgs := ag.executeToolCalls(context.Background(), s, []openrouter.ToolCall{{ID: "1", Type: "function", Function: openrouter.ToolCallFunction{Name: "unknown", Arguments: `{}`}}})
	if len(msgs) != 1 || msgs[0].Role != "tool" {
		t.Fatalf("unexpected msgs: %+v", msgs)
	}
	if !strings.Contains(*msgs[0].Content, "unknown tool") {
		t.Fatalf("expected unknown tool error, got %q", *msgs[0].Content)
	}

	msgs = ag.executeToolCalls(context.Background(), s, []openrouter.ToolCall{{ID: "2", Type: "function", Function: openrouter.ToolCallFunction{Name: "known", Arguments: `{}`}}})
	if !strings.Contains(*msgs[0].Content, "tool not enabled") {
		t.Fatalf("expected not enabled error, got %q", *msgs[0].Content)
	}
}

func TestExecuteToolCalls_AuditsArgsHashOnly(t *testing.T) {
	db := testutil.OpenTestDB(t)
	secretArgs := `{"x":"supersecret"}`
	sum := sha256.Sum256([]byte(secretArgs))
	expectedHash := hex.EncodeToString(sum[:])

	ag := &Agent{toolImpl: map[string]toolset.Tool{"t": dummyTool{name: "t"}}}
	s := toolset.NewSession(tools.NewRegistry())
	s.Active = map[string]bool{"t": true}
	s.DB = db
	s.UserID = 7

	_ = ag.executeToolCalls(context.Background(), s, []openrouter.ToolCall{{ID: "c", Type: "function", Function: openrouter.ToolCallFunction{Name: "t", Arguments: secretArgs}}})

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

type testErr struct{ s string }

func (e *testErr) Error() string { return e.s }
