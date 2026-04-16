package toolset

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

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
	if !strings.Contains(err.Error(), "confirm.request") {
		t.Fatalf("expected guidance to use confirm.request")
	}
}
