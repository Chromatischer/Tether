package userspace

import (
	"os"
	"strings"
	"testing"

	"tether/internal/personality"
)

func TestEnsurePersonalityFile(t *testing.T) {
	d := ForUser(t.TempDir(), 42)
	if err := Ensure(d); err != nil {
		t.Fatal(err)
	}
	if err := EnsurePersonalityFile(d, personality.AgentChat); err != nil {
		t.Fatal(err)
	}
	p, ok := PersonalityAbsPath(d, personality.AgentChat)
	if !ok {
		t.Fatal("expected personality path")
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	if !strings.Contains(text, "Tether personality file") {
		t.Fatalf("expected managed header, got %q", text)
	}
	if !strings.Contains(text, "You are Tether") {
		t.Fatalf("expected default chat personality, got %q", text)
	}
}
