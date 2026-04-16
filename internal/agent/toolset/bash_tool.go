package toolset

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"tether/internal/redact"
	"tether/internal/sandbox"
	"tether/internal/tools"
)

type Bash struct{}

type bashArgs struct {
	Command      string `json:"command"`
	ConfirmToken string `json:"confirm_token"`
}

func (t Bash) Spec() tools.ToolSpec {
	return tools.ToolSpec{
		Name:    "bash",
		Summary: "Run a shell command inside the user sandbox (no network).",
		WhenToUse: "Use this for project introspection (ls/rg/go test), formatting, and other local automation. " +
			"The sandbox has no network access. The working directory is usually workspace/.",
		Safety: "Commands that look destructive (rm/mv/chmod/...) require a confirm_token. Prefer non-destructive commands.",
		InputSchema: map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"command":       map[string]any{"type": "string", "minLength": 1, "description": "shell command to run"},
				"confirm_token": map[string]any{"type": "string", "description": "required for destructive commands"},
			},
			"required": []string{"command"},
		},
		OutputSchema: map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"exit_code":        map[string]any{"type": "integer"},
				"stdout":           map[string]any{"type": "string"},
				"stderr":           map[string]any{"type": "string"},
				"stdout_truncated": map[string]any{"type": "boolean"},
				"stderr_truncated": map[string]any{"type": "boolean"},
				"stdout_max_bytes": map[string]any{"type": "integer"},
				"stderr_max_bytes": map[string]any{"type": "integer"},
			},
			"required": []string{"exit_code", "stdout", "stderr"},
		},
		Examples: []tools.ToolExample{
			{
				Title: "List files",
				Args:  map[string]any{"command": "ls"},
				Result: map[string]any{
					"exit_code": 0,
					"stdout":    "...",
					"stderr":    "",
				},
				Notes: "For destructive commands like rm, first call confirm.request using the scope shown in the error message.",
			},
		},
		Tags: []string{"shell", "sandbox"},
	}
}

func (t Bash) Definition() ToolDef {
	spec := t.Spec()
	return ToolDef{Name: spec.Name, Description: tools.LLMDescription(spec), Parameters: spec.InputSchema}
}

func (t Bash) Execute(ctx context.Context, s *Session, rawArgs json.RawMessage) (any, error) {
	var args bashArgs
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return nil, err
	}
	cmd := strings.TrimSpace(args.Command)
	if cmd == "" {
		return nil, fmt.Errorf("command required")
	}

	if looksDestructive(cmd) {
		scope := bashConfirmScope(cmd)
		if s.Confirm == nil || !s.Confirm.Consume(s.UserID, strings.TrimSpace(args.ConfirmToken), scope) {
			return nil, fmt.Errorf("destructive command requires confirmation; call confirm.request with scope=%q and ask user to /confirm <token>", scope)
		}
	}

	// Mount the whole per-user root so the agent can work with workspace/config/skills.
	res, err := sandbox.RunNoNet(ctx, s.Dirs.Root, []string{"bash", "-lc", "cd workspace 2>/dev/null || true; " + cmd})
	if err != nil {
		return nil, err
	}
	stdout, _ := redact.ScanAndRedact(res.Stdout)
	stderr, _ := redact.ScanAndRedact(res.Stderr)
	return map[string]any{
		"exit_code":        res.ExitCode,
		"stdout":           stdout,
		"stderr":           stderr,
		"stdout_truncated": res.StdoutTruncated,
		"stderr_truncated": res.StderrTruncated,
		"stdout_max_bytes": res.StdoutMaxBytes,
		"stderr_max_bytes": res.StderrMaxBytes,
	}, nil
}

func looksDestructive(cmd string) bool {
	c := strings.ToLower(cmd)
	// Very small heuristic. Expand carefully.
	for _, kw := range []string{"rm ", "rm\t", "mv ", "mv\t", "chmod ", "chown ", "sed -i", "truncate ", "dd if=", "mkfs", "shutdown", "reboot"} {
		if strings.Contains(c, kw) {
			return true
		}
	}
	return false
}

func bashConfirmScope(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	// Hash so the scope is stable and not overly long.
	sum := sha256.Sum256([]byte(cmd))
	h := hex.EncodeToString(sum[:8])
	preview := strings.ReplaceAll(cmd, "\n", " ")
	preview = strings.TrimSpace(preview)
	if len(preview) > 80 {
		preview = preview[:80] + "…"
	}
	if preview == "" {
		preview = "(empty)"
	}
	return "bash:destructive:" + h + ":" + preview
}
