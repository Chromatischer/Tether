package toolset

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"tether/internal/tools"

	"golang.org/x/sys/unix"
)

// ReadFile reads a file within the user root directory.
type ReadFile struct{}

type readFileArgs struct {
	Path string `json:"path"`
}

func (t ReadFile) Spec() tools.ToolSpec {
	return tools.ToolSpec{
		Name:     "read",
		Category: tools.CategoryFiles,
		Summary:  "Read a file from the user sandbox (relative path).",
		WhenToUse: "Use this to inspect code/config/data inside the sandbox. " +
			"Output is truncated (currently ~32KiB) to protect context size.",
		Safety: "Read-only. Cannot access absolute paths; path must stay within the sandbox.",
		InputSchema: map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"path": map[string]any{"type": "string", "minLength": 1, "description": "relative path under the user sandbox root"},
			},
			"required": []string{"path"},
		},
		OutputSchema: map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"path":    map[string]any{"type": "string"},
				"content": map[string]any{"type": "string", "description": "file content (may be truncated)"},
			},
			"required": []string{"path", "content"},
		},
		Examples: []tools.ToolExample{
			{
				Title:  "Read a project file",
				Args:   map[string]any{"path": "workspace/README.md"},
				Result: map[string]any{"path": "workspace/README.md", "content": "..."},
				Notes:  "If the file is large, the tool will truncate content.",
			},
		},
		Tags: []string{"fs", "sandbox"},
	}
}

func (t ReadFile) Definition() ToolDef {
	spec := t.Spec()
	return ToolDef{Name: spec.Name, Description: tools.LLMDescription(spec), Parameters: spec.InputSchema}
}

func (t ReadFile) Execute(ctx context.Context, s *Session, rawArgs json.RawMessage) (any, error) {
	_ = ctx
	var args readFileArgs
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return nil, err
	}
	p, err := resolveUnderRoot(s.Dirs.Root, args.Path)
	if err != nil {
		return nil, err
	}
	relPath := normalizeSandboxRelPath(args.Path)
	// Open with O_NOFOLLOW as a best-effort guard against symlink races.
	fd, err := unix.Open(p, unix.O_RDONLY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), "read")
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("not a regular file")
	}

	// Limit output to avoid huge context.
	const max = 32 * 1024
	b, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, err
	}
	out := string(b)
	if len(out) > max {
		out = out[:max] + "\n... (truncated)"
	}
	s.MarkReadPath(relPath)
	return map[string]any{"path": args.Path, "content": out}, nil
}

// WriteFile writes a file within the user root directory.
type WriteFile struct{}

type writeFileArgs struct {
	Path         string `json:"path"`
	Content      string `json:"content"`
	ConfirmToken string `json:"confirm_token"`
}

func (t WriteFile) Spec() tools.ToolSpec {
	return tools.ToolSpec{
		Name:     "write",
		Category: tools.CategoryFiles,
		Summary:  "Write a file in the user sandbox (relative path).",
		WhenToUse: "Use this to create new files or update files. Prefer small, targeted writes. " +
			"If the file already exists, read it first in the same session so you have current context before overwriting it.",
		Safety: "Creating new files is allowed. Overwriting an existing file requires that the same session has already read that path. Personality files under config/agents/**/PERSONALITY.md remain self-editable and keep backups. Symlinks are rejected.",
		InputSchema: map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"path":          map[string]any{"type": "string", "minLength": 1, "description": "relative path under the user sandbox root"},
				"content":       map[string]any{"type": "string", "description": "full file content to write"},
				"confirm_token": map[string]any{"type": "string", "description": "deprecated; ignored by the write tool"},
			},
			"required": []string{"path", "content"},
		},
		OutputSchema: map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"path":    map[string]any{"type": "string"},
				"written": map[string]any{"type": "boolean"},
			},
			"required": []string{"path", "written"},
		},
		Examples: []tools.ToolExample{
			{
				Title: "Create a new file",
				Args:  map[string]any{"path": "workspace/notes/todo.md", "content": "- item 1\n- item 2\n"},
				Result: map[string]any{
					"path":    "workspace/notes/todo.md",
					"written": true,
				},
			},
		},
		Tags: []string{"fs", "sandbox"},
	}
}

func (t WriteFile) Definition() ToolDef {
	spec := t.Spec()
	return ToolDef{Name: spec.Name, Description: tools.LLMDescription(spec), Parameters: spec.InputSchema}
}

func (t WriteFile) Execute(ctx context.Context, s *Session, rawArgs json.RawMessage) (any, error) {
	_ = ctx
	var args writeFileArgs
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return nil, err
	}
	p, err := resolveUnderRoot(s.Dirs.Root, args.Path)
	if err != nil {
		return nil, err
	}
	relPath := normalizeSandboxRelPath(args.Path)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return nil, err
	}
	if fi, err := os.Lstat(p); err == nil {
		if fi.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("symlink target not allowed")
		}
		if !fi.Mode().IsRegular() {
			return nil, fmt.Errorf("not a regular file")
		}

		if !s.HasReadPath(relPath) {
			return nil, fmt.Errorf("overwriting existing file requires reading it first in this session: %q", relPath)
		}

		if isPersonalityRelPath(args.Path) {
			_ = backupExistingPersonality(p)
		}
	}
	fd, err := unix.Open(p, unix.O_WRONLY|unix.O_CREAT|unix.O_TRUNC|unix.O_NOFOLLOW, 0o644)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), "write")
	defer f.Close()
	if _, err := f.Write([]byte(args.Content)); err != nil {
		return nil, err
	}
	return map[string]any{"path": args.Path, "written": true}, nil
}

func isPersonalityRelPath(relPath string) bool {
	relPath = strings.TrimSpace(relPath)
	clean := filepath.ToSlash(filepath.Clean(relPath))
	// Must be under config/agents/... and end in /PERSONALITY.md
	if !strings.HasPrefix(clean, "config/agents/") {
		return false
	}
	if !strings.HasSuffix(clean, "/PERSONALITY.md") {
		return false
	}
	// Basic sanity: reject weird segments.
	if strings.Contains(clean, "..") {
		return false
	}
	return true
}

func backupExistingPersonality(absPath string) error {
	b, err := os.ReadFile(absPath)
	if err != nil {
		return err
	}
	dir := filepath.Join(filepath.Dir(absPath), ".history")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	name := time.Now().UTC().Format("20060102T150405.000000000Z") + ".md"
	return os.WriteFile(filepath.Join(dir, name), b, 0o644)
}

func writeConfirmScope(relPath string) string {
	relPath = strings.TrimSpace(relPath)
	clean := filepath.ToSlash(filepath.Clean(relPath))
	sum := sha256.Sum256([]byte(clean))
	h := hex.EncodeToString(sum[:8])
	preview := clean
	if len(preview) > 100 {
		preview = preview[:100] + "…"
	}
	if preview == "" {
		preview = "(empty)"
	}
	return "write:overwrite:" + h + ":" + preview
}

func normalizeSandboxRelPath(relPath string) string {
	relPath = strings.TrimSpace(relPath)
	if relPath == "/work" {
		return ""
	}
	if strings.HasPrefix(relPath, "/work/") {
		relPath = strings.TrimPrefix(relPath, "/work/")
	}
	return filepath.ToSlash(filepath.Clean(relPath))
}

func resolveUnderRoot(root, rel string) (string, error) {
	rel = strings.TrimSpace(rel)
	if rel == "" {
		return "", fmt.Errorf("path required")
	}
	if filepath.IsAbs(rel) {
		// Claude Code skills often use ${CLAUDE_SKILL_DIR} which is an absolute sandbox path.
		// In Tether we accept /work/... as a safe alias for a path under the user root.
		if rel == "/work" {
			return "", fmt.Errorf("path required")
		}
		if strings.HasPrefix(rel, "/work/") {
			rel = strings.TrimPrefix(rel, "/work/")
		} else {
			return "", fmt.Errorf("path must be relative")
		}
	}

	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	rootAbs = filepath.Clean(rootAbs)
	if !strings.HasSuffix(rootAbs, string(filepath.Separator)) {
		rootAbs += string(filepath.Separator)
	}

	clean := filepath.Clean(rel)
	if clean == "." || clean == "" {
		return "", fmt.Errorf("path required")
	}
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes sandbox")
	}

	parts := strings.Split(clean, string(filepath.Separator))
	cur := strings.TrimSuffix(rootAbs, string(filepath.Separator))
	for _, part := range parts {
		if part == "" || part == "." {
			continue
		}
		next := filepath.Join(cur, part)
		if fi, err := os.Lstat(next); err == nil {
			if fi.Mode()&os.ModeSymlink != 0 {
				return "", fmt.Errorf("symlink component not allowed")
			}
		}
		cur = next
	}

	fullClean := filepath.Clean(cur)
	if !strings.HasPrefix(fullClean+string(filepath.Separator), rootAbs) {
		return "", fmt.Errorf("path escapes sandbox")
	}
	return fullClean, nil
}
