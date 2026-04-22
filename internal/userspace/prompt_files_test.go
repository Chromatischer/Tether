package userspace

import (
	"os"
	"strings"
	"testing"

	"tether/internal/systemprompt"
)

func TestEnsurePromptTemplateFile(t *testing.T) {
	d := ForUser(t.TempDir(), 42)
	if err := Ensure(d); err != nil {
		t.Fatal(err)
	}
	if err := EnsurePromptTemplateFile(d, systemprompt.TemplateChat); err != nil {
		t.Fatal(err)
	}
	p, ok := PromptTemplateAbsPath(d, systemprompt.TemplateChat)
	if !ok {
		t.Fatal("expected prompt template path")
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	if !strings.Contains(text, "Tether system prompt template") {
		t.Fatalf("expected managed header, got %q", text)
	}
	if !strings.Contains(text, "You are Tether") {
		t.Fatalf("expected default chat prompt content, got %q", text)
	}
}
