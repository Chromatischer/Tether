package proactive

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"tether/internal/sandbox"
	"tether/internal/store"
	"tether/internal/userspace"
)

const conditionTimeout = 10 * time.Second

// conditionMet evaluates an agent's gating predicate. Agents without a
// condition always pass. The predicate is run in the no-network sandbox with
// the user's root mounted at /work; exit code 0 means the condition is met.
//
// It fails closed: any error (no command, sandbox unavailable, timeout) returns
// false so a broken predicate never causes an unintended proactive run.
func (e *Engine) conditionMet(ctx context.Context, userID int64, ar AgentRule) bool {
	if ar.Condition == nil {
		return true
	}
	cmd, err := e.conditionCommand(userID, ar)
	if err != nil || strings.TrimSpace(cmd) == "" {
		e.auditConditionSkip(userID, ar, "no_command", err)
		return false
	}

	dirs := userspace.ForUser(e.dataDir, userID)
	ctx2, cancel := context.WithTimeout(ctx, conditionTimeout)
	defer cancel()

	res, err := sandbox.Run(ctx2, dirs.Root, []string{"bash", "-lc", cmd}, true /* no network */)
	if err != nil {
		e.auditConditionSkip(userID, ar, "sandbox_error", err)
		return false
	}
	return res.ExitCode == 0
}

// conditionCommand resolves the shell command to evaluate. An inline command
// wins; otherwise the script path (relative to the agent folder) is mapped into
// the sandbox at /work/<relpath-from-root> and executed with bash.
func (e *Engine) conditionCommand(userID int64, ar AgentRule) (string, error) {
	if c := strings.TrimSpace(ar.Condition.Command); c != "" {
		return c, nil
	}
	script := strings.TrimSpace(ar.Condition.Script)
	if script == "" {
		return "", nil
	}
	if strings.TrimSpace(ar.Dir) == "" {
		return "", fmt.Errorf("condition script set but agent has no folder")
	}
	// Clean the script path and keep it inside the agent folder.
	clean := filepath.Clean(filepath.FromSlash(script))
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("invalid condition script path: %q", script)
	}
	hostPath := filepath.Join(ar.Dir, clean)

	root := userspace.ForUser(e.dataDir, userID).Root
	rel, err := filepath.Rel(root, hostPath)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("condition script escapes user root")
	}
	sandboxPath := "/work/" + filepath.ToSlash(rel)
	return "bash '" + sandboxPath + "'", nil
}

func (e *Engine) auditConditionSkip(userID int64, ar AgentRule, reason string, err error) {
	if e.db == nil {
		return
	}
	payload := map[string]any{"agent_id": ar.ID, "reason": reason}
	if err != nil {
		payload["error"] = err.Error()
	}
	b, _ := json.Marshal(payload)
	_ = store.AddAuditEvent(e.db, &userID, "proactive_condition_skip", string(b))
}

// conditionTrigger produces a dedup/trigger key for condition-only runs that is
// stable within the agent's cooldown window, so repeated polls inside one window
// dedup to a single run while max_per_day still caps the daily total.
func conditionTrigger(now time.Time, ar AgentRule) string {
	win := ar.CooldownMinutes
	if win <= 0 {
		win = 60
	}
	bucket := now.UTC().Unix() / int64(win*60)
	return fmt.Sprintf("condition:%d", bucket)
}
