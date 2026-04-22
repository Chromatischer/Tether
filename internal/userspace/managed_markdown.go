package userspace

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func ensureManagedMarkdownFile(path string, label string, body string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(renderManagedMarkdown(label, body, time.Now().UTC())), 0o644)
}

func renderManagedMarkdown(label string, body string, now time.Time) string {
	stamp := now.UTC().Format(time.RFC3339)
	return "<!-- " + strings.TrimSpace(label) + " (auto-created: " + stamp + ") -->\n\n" + strings.TrimSpace(body) + "\n"
}
