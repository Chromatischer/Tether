package userspace

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"tether/internal/systemprompt"
)

// PromptTemplateRelPath returns the sandbox-relative path for a system prompt template.
// Example: config/prompts/chat_system.md
func PromptTemplateRelPath(name string) (string, bool) {
	switch strings.TrimSpace(name) {
	case systemprompt.TemplateChat:
		return filepath.ToSlash(filepath.Join("config", "prompts", "chat_system.md")), true
	case systemprompt.TemplateProactive:
		return filepath.ToSlash(filepath.Join("config", "prompts", "proactive_system.md")), true
	default:
		return "", false
	}
}

// PromptTemplateAbsPath returns the absolute path for a system prompt template.
func PromptTemplateAbsPath(d Dirs, name string) (string, bool) {
	rel, ok := PromptTemplateRelPath(name)
	if !ok {
		return "", false
	}
	return filepath.Join(d.Root, filepath.FromSlash(rel)), true
}

// EnsurePromptTemplateFile creates the prompt template file if it doesn't exist.
// It never overwrites an existing file.
func EnsurePromptTemplateFile(d Dirs, name string) error {
	p, ok := PromptTemplateAbsPath(d, name)
	if !ok {
		return nil
	}
	if _, err := os.Stat(p); err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	content := systemprompt.DefaultMarkdown(name)
	stamp := time.Now().UTC().Format(time.RFC3339)
	content = "<!-- Tether system prompt template (auto-created: " + stamp + ") -->\n\n" + strings.TrimSpace(content) + "\n"
	return os.WriteFile(p, []byte(content), 0o644)
}
