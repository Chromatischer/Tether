package toolset

import (
	"context"
	"encoding/json"
	"runtime"
	"strings"
	"testing"
)

func TestAdminBash_RequiresJustification(t *testing.T) {
	tool := AdminBash{}
	s := &Session{UserID: 1, Confirm: &stubConfirmer{ok: true}}
	args, _ := json.Marshal(map[string]any{"command": "echo hi"})
	_, err := tool.Execute(context.Background(), s, args)
	if err == nil || !strings.Contains(err.Error(), "justification required") {
		t.Fatalf("expected justification error, got: %v", err)
	}
}

func TestAdminBash_RequiresApproval(t *testing.T) {
	tool := AdminBash{}
	// No confirmer / no token => must refuse with a confirmation scope.
	s := &Session{UserID: 1}
	args, _ := json.Marshal(map[string]any{"command": "echo hi", "justification": "needed on host"})
	_, err := tool.Execute(context.Background(), s, args)
	if err == nil {
		t.Fatalf("expected approval error")
	}
	if !strings.Contains(err.Error(), "requires user approval") || !strings.Contains(err.Error(), "scope=") {
		t.Fatalf("expected approval scope error, got: %v", err)
	}
}

func TestAdminBash_RunsAfterApproval(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("needs a POSIX shell")
	}
	tool := AdminBash{}
	s := &Session{UserID: 1, Confirm: &stubConfirmer{ok: true}}
	args, _ := json.Marshal(map[string]any{
		"command":       "echo admin-ok",
		"justification": "smoke test",
		"confirm_token": "tok",
	})
	res, err := tool.Execute(context.Background(), s, args)
	if err != nil {
		t.Fatalf("expected success after approval, got: %v", err)
	}
	m := res.(map[string]any)
	if m["exit_code"].(int) != 0 {
		t.Fatalf("expected exit 0, got %v", m["exit_code"])
	}
	if !strings.Contains(m["stdout"].(string), "admin-ok") {
		t.Fatalf("expected command output, got %q", m["stdout"])
	}
}

func TestAdminBashScope_StableAndPreviewed(t *testing.T) {
	a := adminBashScope("rm -rf /tmp/x")
	b := adminBashScope("rm -rf /tmp/x")
	if a != b {
		t.Fatalf("scope not stable")
	}
	if !strings.HasPrefix(a, "admin-bash:") || !strings.Contains(a, "rm -rf /tmp/x") {
		t.Fatalf("unexpected scope: %q", a)
	}
}
