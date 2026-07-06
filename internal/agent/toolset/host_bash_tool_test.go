package toolset

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"tether/internal/userspace"
)

// scopeConfirmer approves a single (token, scope) pair, mimicking the real
// confirm flow after the user has approved.
type scopeConfirmer struct {
	token string
	scope string
}

func (c scopeConfirmer) Request(_ int64, scope string, _ string) string { return "minted" }
func (c scopeConfirmer) Consume(_ int64, token string, scope string) bool {
	return token != "" && token == c.token && scope == c.scope
}

func runHostBash(t *testing.T, s *Session, args hostBashArgs) (any, error) {
	t.Helper()
	raw, _ := json.Marshal(args)
	return HostBash{}.Execute(context.Background(), s, raw)
}

func TestHostBash_SubagentRejected(t *testing.T) {
	s := &Session{IsSubagent: true, AllowHostExec: true}
	if _, err := runHostBash(t, s, hostBashArgs{Command: "echo hi", Reason: "need host access"}); err == nil {
		t.Fatal("expected sub-agent rejection")
	}
}

func TestHostBash_DisabledRejected(t *testing.T) {
	s := &Session{AllowHostExec: false}
	_, err := runHostBash(t, s, hostBashArgs{Command: "echo hi", Reason: "need host access"})
	if err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("expected disabled rejection, got %v", err)
	}
}

func TestHostBash_ReasonRequired(t *testing.T) {
	s := &Session{AllowHostExec: true}
	if _, err := runHostBash(t, s, hostBashArgs{Command: "echo hi", Reason: "short"}); err == nil {
		t.Fatal("expected reason-length rejection")
	}
}

func TestHostBash_RequiresConfirm(t *testing.T) {
	s := &Session{AllowHostExec: true, Confirm: scopeConfirmer{}}
	_, err := runHostBash(t, s, hostBashArgs{Command: "echo hi", Reason: "need host access"})
	if err == nil || !strings.Contains(err.Error(), "scope=") {
		t.Fatalf("expected confirmation scope error, got %v", err)
	}
}

func TestHostBash_RunsWithConfirm(t *testing.T) {
	root := t.TempDir()
	cmd := "echo host-ok"
	reason := "need host access"
	scope := hostBashConfirmScope(cmd, reason)

	s := &Session{
		AllowHostExec: true,
		Dirs:          userspace.Dirs{Root: root},
		Confirm:       scopeConfirmer{token: "good", scope: scope},
	}
	out, err := runHostBash(t, s, hostBashArgs{Command: cmd, Reason: reason, ConfirmToken: "good"})
	if err != nil {
		t.Fatal(err)
	}
	m := out.(map[string]any)
	if m["exit_code"].(int) != 0 {
		t.Fatalf("exit_code = %v", m["exit_code"])
	}
	if !strings.Contains(m["stdout"].(string), "host-ok") {
		t.Fatalf("stdout = %q", m["stdout"])
	}
}
