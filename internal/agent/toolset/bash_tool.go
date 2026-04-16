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
)

type Bash struct{}

type bashArgs struct {
	Command      string `json:"command"`
	ConfirmToken string `json:"confirm_token"`
}

func (t Bash) Definition() ToolDef {
	return ToolDef{
		Name:        "bash",
		Description: "Run a shell command inside the user sandbox (no network). Root contains workspace/, config/, skills/, cache/.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command":       map[string]any{"type": "string"},
				"confirm_token": map[string]any{"type": "string", "description": "required for destructive commands"},
			},
			"required": []string{"command"},
		},
	}
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
