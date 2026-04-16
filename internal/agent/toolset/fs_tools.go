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

	"golang.org/x/sys/unix"
)

// ReadFile reads a file within the user root directory.
type ReadFile struct{}

type readFileArgs struct {
	Path string `json:"path"`
}

func (t ReadFile) Definition() ToolDef {
	return ToolDef{
		Name:        "read",
		Description: "Read a file from the user sandbox (path is relative to the user root).",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{"type": "string"},
			},
			"required": []string{"path"},
		},
	}
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
	return map[string]any{"path": args.Path, "content": out}, nil
}

// WriteFile writes a file within the user root directory.
type WriteFile struct{}

type writeFileArgs struct {
	Path         string `json:"path"`
	Content      string `json:"content"`
	ConfirmToken string `json:"confirm_token"`
}

func (t WriteFile) Definition() ToolDef {
	return ToolDef{
		Name:        "write",
		Description: "Write a file in the user sandbox (path is relative to the user root).",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":          map[string]any{"type": "string"},
				"content":       map[string]any{"type": "string"},
				"confirm_token": map[string]any{"type": "string", "description": "required when overwriting an existing file"},
			},
			"required": []string{"path", "content"},
		},
	}
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
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return nil, err
	}
	if fi, err := os.Lstat(p); err == nil {
		// Overwriting an existing file is considered destructive.
		scope := writeConfirmScope(args.Path)
		if s.Confirm == nil || !s.Confirm.Consume(s.UserID, strings.TrimSpace(args.ConfirmToken), scope) {
			return nil, fmt.Errorf("overwriting existing file requires confirmation; call confirm.request with scope=%q and ask user to /confirm <token>", scope)
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("symlink target not allowed")
		}
		if !fi.Mode().IsRegular() {
			return nil, fmt.Errorf("not a regular file")
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

func resolveUnderRoot(root, rel string) (string, error) {
	rel = strings.TrimSpace(rel)
	if rel == "" {
		return "", fmt.Errorf("path required")
	}
	if filepath.IsAbs(rel) {
		return "", fmt.Errorf("path must be relative")
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
