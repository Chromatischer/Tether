package toolset

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"tether/internal/tools"
)

type stubConfirmer struct {
	ok bool
}

func (c *stubConfirmer) Request(userID int64, scope string, reason string) string { return "tok" }
func (c *stubConfirmer) Consume(userID int64, token string, scope string) bool    { return c.ok }

func TestLooksDestructive(t *testing.T) {
	if !looksDestructive("rm -rf .") {
		t.Fatalf("expected rm to be destructive")
	}
	if looksDestructive("echo rm -rf") {
		// heuristic checks for "rm " substring; this should still be destructive.
		// We'll accept it to stay conservative.
	}
	if looksDestructive("echo hello") {
		t.Fatalf("expected non-destructive")
	}
}

func TestBashConfirmScope_StableAndContainsPreview(t *testing.T) {
	cmd := "rm -rf /tmp/test"
	s1 := bashConfirmScope(cmd)
	s2 := bashConfirmScope(cmd)
	if s1 != s2 {
		t.Fatalf("expected stable scope")
	}
	if !strings.HasPrefix(s1, "bash:destructive:") {
		t.Fatalf("unexpected scope prefix: %q", s1)
	}
	if !strings.Contains(s1, "rm -rf /tmp/test") {
		t.Fatalf("expected preview in scope: %q", s1)
	}
}

func TestBashExecute_DestructiveRequiresConfirmation(t *testing.T) {
	tool := Bash{}
	s := &Session{UserID: 1}
	args, _ := json.Marshal(map[string]any{"command": "rm -rf /"})
	_, err := tool.Execute(context.Background(), s, args)
	if err == nil {
		t.Fatalf("expected error")
	}
	if !strings.Contains(err.Error(), "requires confirmation") {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(err.Error(), "scope=") {
		t.Fatalf("expected error to include confirmation scope")
	}
}

func TestBashEnableNetworkScope_Stable(t *testing.T) {
	if bashEnableNetworkScope() != "tool.enable:bash:network" {
		t.Fatalf("unexpected scope: %q", bashEnableNetworkScope())
	}
}

func TestToolEnable_BashNetworkRequiresConfirmation(t *testing.T) {
	tool := ToolEnable{}
	s := &Session{UserID: 1}
	args, _ := json.Marshal(map[string]any{"category": "exec", "network": true})
	_, err := tool.Execute(context.Background(), s, args)
	if err == nil {
		t.Fatalf("expected error")
	}
	if !strings.Contains(err.Error(), "requires confirmation") {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(err.Error(), "scope=") {
		t.Fatalf("expected confirmation scope in error: %v", err)
	}
}

func TestToolEnable_BashNetworkSetsSessionFlag(t *testing.T) {
	tool := ToolEnable{}
	s := NewSession(nil)
	s.UserID = 1
	s.Registry = nil
	s.Active = map[string]bool{}
	s.Confirm = &stubConfirmer{ok: true}
	s.Registry = toolsTestRegistry()
	args, _ := json.Marshal(map[string]any{"category": "exec", "network": true, "confirm_token": "tok"})
	got, err := tool.Execute(context.Background(), s, args)
	if err != nil {
		t.Fatalf("expected success, got: %v", err)
	}
	if !s.BashNetworkEnabled {
		t.Fatalf("expected bash network flag enabled")
	}
	m, _ := got.(map[string]any)
	enabled, _ := m["enabled"].([]string)
	hasBash := false
	for _, e := range enabled {
		if e == "bash" {
			hasBash = true
		}
	}
	if !hasBash {
		t.Fatalf("expected bash among enabled tools: %#v", got)
	}
	if network, _ := m["network"].(bool); !network {
		t.Fatalf("expected network=true in result: %#v", got)
	}
}

func toolsTestRegistry() *tools.Registry {
	reg := tools.NewRegistry()
	for _, impl := range DefaultTools() {
		reg.Register(impl.Spec())
	}
	return reg
}
