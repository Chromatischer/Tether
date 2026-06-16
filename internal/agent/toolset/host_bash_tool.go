package toolset

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"tether/internal/redact"
	"tether/internal/store"
	"tether/internal/tools"
)

const (
	hostBashTimeout      = 120 * time.Second
	hostBashMaxOutput    = 100_000
	hostBashMinReasonLen = 8
)

// HostBash runs a shell command directly on the host, OUTSIDE the bubblewrap
// sandbox. It is the deliberate escape hatch for the rare case that genuinely
// needs host access. It is locked behind three gates:
//   - an admin must enable host execution globally (AllowHostExec), default off;
//   - the caller must give a specific, non-trivial reason;
//   - every command requires a per-call /confirm approval bound to that exact
//     command and reason.
//
// It is never available to sub-agents.
type HostBash struct{}

type hostBashArgs struct {
	Command      string `json:"command"`
	Reason       string `json:"reason"`
	ConfirmToken string `json:"confirm_token"`
}

func (t HostBash) Spec() tools.ToolSpec {
	return tools.ToolSpec{
		Name: "bash.host",
		Summary: "Run a shell command directly on the host, OUTSIDE the sandbox. " +
			"Disabled unless an admin enabled host execution; every call needs a reason and a per-call confirmation.",
		WhenToUse: "Use only when a task genuinely requires host access that the sandboxed bash tool cannot provide " +
			"(real network/host state, host services, files outside the user sandbox). Prefer the sandboxed bash tool for everything else. " +
			"This tool is disabled by default and must be enabled via tool.enable; it is not available to sub-agents.",
		Safety: "Runs unsandboxed on the host with full host access and inherits the host environment. " +
			"Requires (1) admin-enabled host execution, (2) a specific reason, and (3) a confirm_token approving this exact command. " +
			"The command and reason are recorded in the audit log.",
		InputSchema: map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"command": map[string]any{"type": "string", "minLength": 1, "description": "shell command to run on the host"},
				"reason": map[string]any{
					"type":        "string",
					"minLength":   hostBashMinReasonLen,
					"description": "specific justification for why host (non-sandboxed) access is required",
				},
				"confirm_token": map[string]any{"type": "string", "description": "required; approves this exact command + reason"},
			},
			"required": []string{"command", "reason"},
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
			},
			"required": []string{"exit_code", "stdout", "stderr"},
		},
		Examples: []tools.ToolExample{
			{
				Title:  "Run a host command with reason and approval",
				Args:   map[string]any{"command": "systemctl is-active nginx", "reason": "check whether the host nginx service is running", "confirm_token": "<token>"},
				Result: map[string]any{"exit_code": 0, "stdout": "active\n", "stderr": ""},
				Notes:  "Without a valid confirm_token the host pauses and asks the user to /confirm before the command runs.",
			},
		},
		Tags: []string{"shell", "host", "dangerous"},
	}
}

func (t HostBash) Definition() ToolDef {
	spec := t.Spec()
	return ToolDef{Name: spec.Name, Description: tools.LLMDescription(spec), Parameters: spec.InputSchema}
}

func (t HostBash) Execute(ctx context.Context, s *Session, rawArgs json.RawMessage) (any, error) {
	if s == nil {
		return nil, errors.New("session not available")
	}
	if s.IsSubagent {
		return nil, errors.New("bash.host is not allowed in sub-agent context")
	}
	if !s.AllowHostExec {
		return nil, errors.New("non-sandboxed host execution is disabled; an admin must enable it in admin settings (agent tab) before this tool can run")
	}

	var args hostBashArgs
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return nil, err
	}
	cmd := strings.TrimSpace(args.Command)
	if cmd == "" {
		return nil, fmt.Errorf("command required")
	}
	reason := strings.TrimSpace(args.Reason)
	if len(reason) < hostBashMinReasonLen {
		return nil, fmt.Errorf("a specific reason (at least %d characters) is required to run a host command", hostBashMinReasonLen)
	}

	// Every host command requires a per-call confirmation bound to this exact
	// command and reason. The scope is recomputed identically on resume, so the
	// injected token consumes against the same scope.
	scope := hostBashConfirmScope(cmd, reason)
	if s.Confirm == nil || !s.Confirm.Consume(s.UserID, strings.TrimSpace(args.ConfirmToken), scope) {
		return nil, fmt.Errorf("non-sandboxed host execution requires confirmation; scope=%q", scope)
	}

	// Audit the privileged run with the command and reason in clear, for accountability.
	if s.DB != nil {
		uid := s.UserID
		payload, _ := json.Marshal(map[string]any{
			"command": truncateForAudit(cmd, 400),
			"reason":  truncateForAudit(reason, 400),
		})
		_ = store.AddAuditEvent(s.DB, &uid, "host_exec", string(payload))
	}

	ctx2, cancel := context.WithTimeout(ctx, hostBashTimeout)
	defer cancel()

	c := exec.CommandContext(ctx2, "bash", "-lc", cmd)
	if dir := strings.TrimSpace(s.Dirs.Root); dir != "" {
		c.Dir = dir
	}
	stdout := &cappedBuffer{max: hostBashMaxOutput}
	stderr := &cappedBuffer{max: hostBashMaxOutput}
	c.Stdout = stdout
	c.Stderr = stderr

	exitCode := 0
	if err := c.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			exitCode = ee.ExitCode()
		} else {
			return nil, err
		}
	}

	outText, _ := redact.ScanAndRedact(stdout.String())
	errText, _ := redact.ScanAndRedact(stderr.String())
	return map[string]any{
		"exit_code":        exitCode,
		"stdout":           outText,
		"stderr":           errText,
		"stdout_truncated": stdout.truncated,
		"stderr_truncated": stderr.truncated,
	}, nil
}

func hostBashConfirmScope(cmd, reason string) string {
	sum := sha256.Sum256([]byte(cmd + "\x00" + reason))
	h := hex.EncodeToString(sum[:8])
	preview := strings.ReplaceAll(strings.TrimSpace(cmd), "\n", " ")
	if len(preview) > 80 {
		preview = preview[:80] + "…"
	}
	if preview == "" {
		preview = "(empty)"
	}
	return "bash.host:" + h + ":" + preview
}

func truncateForAudit(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}

// cappedBuffer collects up to max bytes and then drops the rest, flagging that
// truncation occurred. It never returns an error so the command keeps running.
type cappedBuffer struct {
	buf       []byte
	max       int
	truncated bool
}

func (w *cappedBuffer) Write(p []byte) (int, error) {
	if w.max <= 0 {
		w.max = hostBashMaxOutput
	}
	if remain := w.max - len(w.buf); remain > 0 {
		if len(p) > remain {
			w.buf = append(w.buf, p[:remain]...)
			w.truncated = true
		} else {
			w.buf = append(w.buf, p...)
		}
	} else {
		w.truncated = true
	}
	return len(p), nil
}

func (w *cappedBuffer) String() string { return string(w.buf) }
