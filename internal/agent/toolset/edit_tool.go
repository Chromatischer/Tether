package toolset

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"tether/internal/tools"

	"golang.org/x/sys/unix"
)

// EditFile performs an exact string replacement inside an existing file in the
// user sandbox, leaving the rest of the file untouched. It complements write
// (which replaces whole files) for small, targeted changes.
type EditFile struct{}

type editFileArgs struct {
	Path       string `json:"path"`
	OldString  string `json:"old_string"`
	NewString  string `json:"new_string"`
	ReplaceAll bool   `json:"replace_all"`
}

func (t EditFile) Spec() tools.ToolSpec {
	return tools.ToolSpec{
		Name:    "edit",
		Summary: "Make an exact string replacement in an existing file in the user sandbox.",
		Safety: "Edits an existing file in place. Requires that the same session has already read the path. " +
			"old_string must occur exactly once unless replace_all is true. Symlinks are rejected. " +
			"Personality files under config/agents/**/PERSONALITY.md keep backups.",
		InputSchema: map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"path":        map[string]any{"type": "string", "minLength": 1, "description": "relative path under the user sandbox root"},
				"old_string":  map[string]any{"type": "string", "minLength": 1, "description": "exact text to replace"},
				"new_string":  map[string]any{"type": "string", "description": "text to replace it with (must differ from old_string)"},
				"replace_all": map[string]any{"type": "boolean", "description": "replace every occurrence instead of requiring a unique match (default false)"},
			},
			"required": []string{"path", "old_string", "new_string"},
		},
		OutputSchema: map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"path":         map[string]any{"type": "string"},
				"replacements": map[string]any{"type": "integer"},
			},
			"required": []string{"path", "replacements"},
		},
		Examples: []tools.ToolExample{
			{
				Title:  "Fix a single line",
				Args:   map[string]any{"path": "workspace/main.go", "old_string": "port := 8080", "new_string": "port := 9090"},
				Result: map[string]any{"path": "workspace/main.go", "replacements": 1},
			},
		},
		Tags: []string{"fs", "sandbox"},
	}
}

func (t EditFile) Definition() ToolDef {
	spec := t.Spec()
	return ToolDef{Name: spec.Name, Description: tools.LLMDescription(spec), Parameters: spec.InputSchema}
}

func (t EditFile) Execute(ctx context.Context, s *Session, rawArgs json.RawMessage) (any, error) {
	_ = ctx
	var args editFileArgs
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return nil, err
	}
	if args.OldString == "" {
		return nil, fmt.Errorf("old_string required")
	}
	if args.OldString == args.NewString {
		return nil, fmt.Errorf("old_string and new_string are identical; nothing to change")
	}

	p, err := resolveUnderRoot(s.Dirs.Root, args.Path)
	if err != nil {
		return nil, err
	}
	relPath := normalizeSandboxRelPath(args.Path)

	fi, err := os.Lstat(p)
	if err != nil {
		return nil, err
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("symlink target not allowed")
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("not a regular file")
	}
	if !s.HasReadPath(relPath) {
		return nil, fmt.Errorf("editing a file requires reading it first in this session: %q", relPath)
	}

	b, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	content := string(b)

	count := strings.Count(content, args.OldString)
	if count == 0 {
		return nil, fmt.Errorf("old_string not found in %q; it must match the file exactly, including whitespace", relPath)
	}
	if count > 1 && !args.ReplaceAll {
		return nil, fmt.Errorf("old_string occurs %d times in %q; add surrounding context to make it unique, or set replace_all", count, relPath)
	}

	var updated string
	if args.ReplaceAll {
		updated = strings.ReplaceAll(content, args.OldString, args.NewString)
	} else {
		updated = strings.Replace(content, args.OldString, args.NewString, 1)
	}

	if isPersonalityRelPath(args.Path) {
		_ = backupExistingPersonality(p)
	}

	fd, err := unix.Open(p, unix.O_WRONLY|unix.O_TRUNC|unix.O_NOFOLLOW, 0o644)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), "edit")
	defer f.Close()
	if _, err := f.Write([]byte(updated)); err != nil {
		return nil, err
	}

	replacements := 1
	if args.ReplaceAll {
		replacements = count
	}
	return map[string]any{"path": args.Path, "replacements": replacements}, nil
}
