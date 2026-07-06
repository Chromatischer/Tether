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

// AdminBash is a privileged shell tool that runs commands directly on the host,
// OUTSIDE the bubblewrap sandbox, with the full host environment and filesystem.
//
// It is deliberately heavily gated:
//   - it lives in the off-by-default "admin" category (must be enabled explicitly);
//   - it requires a non-empty `justification`;
//   - EVERY call requires a fresh, single-use user confirmation token. There is no
//     "remember this" — approval is per command.
type AdminBash struct{}

type adminBashArgs struct {
	Command       string `json:"command"`
	Justification string `json:"justification"`
	ConfirmToken  string `json:"confirm_token"`
}

func (t AdminBash) Spec() tools.ToolSpec {
	return tools.ToolSpec{
		Name:     "admin.bash",
		Category: tools.CategoryAdmin,
		Summary:  "Run a shell command on the HOST, outside the sandbox, with full permissions. Requires a justification and explicit per-call user approval.",
		WhenToUse: "Use this ONLY when a task genuinely cannot be done inside the sandboxed `bash` tool — e.g. host administration, " +
			"installing system packages, or accessing files outside the per-user sandbox. Prefer the sandboxed `bash` tool whenever possible. " +
			"You must supply a clear `justification`; the user sees it and must approve each command via /confirm before it runs.",
		Safety: "DANGEROUS. Runs unsandboxed on the host with the full environment, filesystem, and network — there are NO restrictions on what the " +
			"command can do. Every invocation pauses for explicit user approval (a fresh confirm_token, single-use, per command). Destructive or " +
			"irreversible actions are possible; describe exactly what you intend to do in the justification.",
		InputSchema: map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"command":       map[string]any{"type": "string", "minLength": 1, "description": "shell command to run on the host (bash -lc)"},
				"justification": map[string]any{"type": "string", "minLength": 1, "description": "clear, user-facing reason why host (unsandboxed) execution is necessary; shown in the approval prompt"},
				"confirm_token": map[string]any{"type": "string", "description": "(host-injected on resume) the user-approved token; required before the command runs"},
			},
			"required": []string{"command", "justification"},
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
				Title:  "Install a system package on the host",
				Args:   map[string]any{"command": "pacman -S --noconfirm ripgrep", "justification": "Install ripgrep on the host so future searches are faster; sandbox cannot persist packages."},
				Result: map[string]any{"exit_code": 0, "stdout": "...", "stderr": ""},
				Notes:  "The host pauses and asks the user to /confirm <token>. The command runs only after approval.",
			},
		},
		Tags: []string{"shell", "host", "admin", "dangerous"},
	}
}

func (t AdminBash) Definition() ToolDef {
	spec := t.Spec()
	return ToolDef{Name: spec.Name, Description: tools.LLMDescription(spec), Parameters: spec.InputSchema}
}

func (t AdminBash) Execute(ctx context.Context, s *Session, rawArgs json.RawMessage) (any, error) {
	var args adminBashArgs
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return nil, err
	}
	cmd := strings.TrimSpace(args.Command)
	if cmd == "" {
		return nil, fmt.Errorf("command required")
	}
	if strings.TrimSpace(args.Justification) == "" {
		return nil, fmt.Errorf("justification required: explain why unsandboxed host execution is necessary")
	}

	// Every call requires fresh, single-use user approval — no exceptions, no caching.
	scope := adminBashScope(cmd)
	if s.Confirm == nil || !s.Confirm.Consume(s.UserID, strings.TrimSpace(args.ConfirmToken), scope) {
		return nil, fmt.Errorf("admin.bash requires user approval; scope=%q", scope)
	}

	res, err := sandbox.RunHost(ctx, "", []string{"bash", "-lc", cmd})
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

// adminBashScope builds a stable, single-action confirmation scope for a command.
func adminBashScope(cmd string) string {
	cmd = strings.TrimSpace(cmd)
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
	return "admin-bash:" + h + ":" + preview
}
