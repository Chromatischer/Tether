package userspace

import (
	"path/filepath"
	"strings"

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
	return ensureManagedMarkdownFile(p, "Tether system prompt template", systemprompt.DefaultMarkdown(name))
}
