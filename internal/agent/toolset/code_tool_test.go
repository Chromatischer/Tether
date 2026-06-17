package toolset

import (
	"context"
	"encoding/json"
	"os/exec"
	"runtime"
	"testing"

	"tether/internal/tools"
)

// fakeInvoker records calls and returns canned results so the code tool can be
// exercised without the full agent runtime.
type fakeInvoker struct {
	calls   []string
	results map[string]any
	errs    map[string]error
}

func (f *fakeInvoker) Invoke(ctx context.Context, s *Session, name string, rawArgs json.RawMessage) (any, error) {
	f.calls = append(f.calls, name)
	if err, ok := f.errs[name]; ok {
		return nil, err
	}
	if r, ok := f.results[name]; ok {
		return r, nil
	}
	return map[string]any{"echo": name, "args": json.RawMessage(rawArgs)}, nil
}

func requireSandbox(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("sandbox is linux-only")
	}
	if _, err := exec.LookPath("bwrap"); err != nil {
		t.Skip("bwrap not available")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
}

func newCodeSession(t *testing.T, inv ToolInvoker) *Session {
	t.Helper()
	reg := tools.NewRegistry()
	reg.Register(ReadFile{}.Spec())
	s := NewSession(reg)
	s.Dirs.Root = t.TempDir()
	s.Tools = inv
	return s
}

func runCode(t *testing.T, s *Session, code string) map[string]any {
	t.Helper()
	args, _ := json.Marshal(map[string]any{"code": code})
	out, err := Code{}.Execute(context.Background(), s, args)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("expected map result, got %T", out)
	}
	if ec, _ := m["exit_code"].(int); ec != 0 {
		t.Fatalf("non-zero exit: %v\nstderr: %v", m["exit_code"], m["stderr"])
	}
	return m
}

// decodeResult decodes the json.RawMessage stored under "result" into a map.
func decodeResult(t *testing.T, m map[string]any) map[string]any {
	t.Helper()
	raw, ok := m["result"].(json.RawMessage)
	if !ok {
		t.Fatalf("result missing or wrong type: %#v", m["result"])
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	return out
}

func TestCode_ResultAndStdout(t *testing.T) {
	requireSandbox(t)
	s := newCodeSession(t, &fakeInvoker{})
	m := runCode(t, s, "print('hello')\nresult({'n': 1 + 2})")

	if got, _ := m["stdout"].(string); got != "hello\n" {
		t.Fatalf("stdout = %q, want %q", got, "hello\n")
	}
	res := decodeResult(t, m)
	if res["n"].(float64) != 3 {
		t.Fatalf("result.n = %v, want 3", res["n"])
	}
}

func TestCode_CallToolBridge(t *testing.T) {
	requireSandbox(t)
	inv := &fakeInvoker{results: map[string]any{
		"read": map[string]any{"content": "file body", "size": 9},
	}}
	s := newCodeSession(t, inv)
	m := runCode(t, s, "r = call_tool('read', path='/work/x')\nresult({'size': r['size']})")

	if len(inv.calls) != 1 || inv.calls[0] != "read" {
		t.Fatalf("invoker calls = %v, want [read]", inv.calls)
	}
	if tc, _ := m["tool_calls"].(int); tc != 1 {
		t.Fatalf("tool_calls = %v, want 1", m["tool_calls"])
	}
	res := decodeResult(t, m)
	if res["size"].(float64) != 9 {
		t.Fatalf("result.size = %v, want 9", res["size"])
	}
}

func TestCode_ToolErrorRaises(t *testing.T) {
	requireSandbox(t)
	inv := &fakeInvoker{errs: map[string]error{"read": context.DeadlineExceeded}}
	s := newCodeSession(t, inv)
	// The ToolError should be catchable in Python and reported via stdout.
	m := runCode(t, s, "try:\n    call_tool('read', path='x')\nexcept ToolError as e:\n    print('caught:', e)")
	if got, _ := m["stdout"].(string); got == "" {
		t.Fatalf("expected stdout from caught error, got empty")
	}
}
