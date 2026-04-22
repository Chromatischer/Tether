package userspace

import (
	"path/filepath"
	"strings"

	"tether/internal/personality"
)

// PersonalityRelPath returns the sandbox-relative path for an agent personality file.
// Example: config/agents/chat/PERSONALITY.md
func PersonalityRelPath(agentKey string) (string, bool) {
	agentKey = strings.TrimSpace(agentKey)
	if !personality.IsValidAgentKey(agentKey) {
		return "", false
	}
	return filepath.ToSlash(filepath.Join("config", "agents", agentKey, "PERSONALITY.md")), true
}

// PersonalityAbsPath returns the absolute path for an agent personality file.
func PersonalityAbsPath(d Dirs, agentKey string) (string, bool) {
	rel, ok := PersonalityRelPath(agentKey)
	if !ok {
		return "", false
	}
	return filepath.Join(d.Root, filepath.FromSlash(rel)), true
}

// EnsurePersonalityFile creates the personality file for the given agent if it doesn't exist.
// It never overwrites an existing file.
func EnsurePersonalityFile(d Dirs, agentKey string) error {
	p, ok := PersonalityAbsPath(d, agentKey)
	if !ok {
		return nil
	}
	return ensureManagedMarkdownFile(p, "Tether personality file", personality.DefaultMarkdown(agentKey))
}
