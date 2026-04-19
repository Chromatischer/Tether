package toolset

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"tether/internal/tools"
)

type subagentStoreStub struct {
	lastUserID int64
	lastReq    SubagentSpawnRequest
}

func (s *subagentStoreStub) Spawn(userID int64, req SubagentSpawnRequest) string {
	s.lastUserID = userID
	s.lastReq = req
	return "run_123"
}

func (s *subagentStoreStub) Status(userID int64, id string) (any, bool) {
	return nil, false
}

func TestSubagentSpawnRejectsNestedSubagents(t *testing.T) {
	store := &subagentStoreStub{}
	sess := testSessionWithRegistry()
	sess.Subagents = store
	sess.IsSubagent = true

	_, err := SubagentSpawn{}.Execute(context.Background(), sess, mustJSON(t, map[string]any{
		"prompt": "do work",
	}))
	if err == nil || !strings.Contains(err.Error(), "cannot spawn subagents") {
		t.Fatalf("expected nested subagent error, got %v", err)
	}
}

func TestSubagentSpawnValidatesToolsetAndSkillPreload(t *testing.T) {
	store := &subagentStoreStub{}
	sess := testSessionWithRegistry()
	sess.Subagents = store
	sess.UserID = 42

	_, err := SubagentSpawn{}.Execute(context.Background(), sess, mustJSON(t, map[string]any{
		"prompt":          "review repo",
		"allowed_tools":   []string{"read", "write"},
		"preload_skill":   "repo-review",
		"skill_arguments": "focus on docs",
	}))
	if err != nil {
		t.Fatalf("spawn failed: %v", err)
	}
	if store.lastUserID != 42 {
		t.Fatalf("unexpected user id: %d", store.lastUserID)
	}
	if got := strings.Join(store.lastReq.AllowedTools, ","); got != "read,write" {
		t.Fatalf("unexpected allowed tools: %s", got)
	}
	if store.lastReq.Skill == nil || store.lastReq.Skill.Name != "repo-review" || store.lastReq.Skill.Arguments != "focus on docs" {
		t.Fatalf("unexpected skill preload: %#v", store.lastReq.Skill)
	}
}

func TestSubagentSpawnRejectsSkillInvokeInToolset(t *testing.T) {
	sess := testSessionWithRegistry()
	sess.Subagents = &subagentStoreStub{}

	_, err := SubagentSpawn{}.Execute(context.Background(), sess, mustJSON(t, map[string]any{
		"prompt":        "do work",
		"allowed_tools": []string{"skill.invoke"},
	}))
	if err == nil || !strings.Contains(err.Error(), "cannot invoke skills") {
		t.Fatalf("expected skill.invoke rejection, got %v", err)
	}
}

func TestToolSearchRespectsAllowedToolUniverse(t *testing.T) {
	sess := testSessionWithRegistry()
	sess.Allowed = map[string]bool{"read": true}

	out, err := ToolSearch{}.Execute(context.Background(), sess, mustJSON(t, map[string]any{"query": ""}))
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	infos, ok := out.([]tools.ToolInfo)
	if !ok {
		t.Fatalf("unexpected result type: %T", out)
	}
	if len(infos) != 1 || infos[0].Name != "read" {
		t.Fatalf("unexpected search results: %#v", infos)
	}
}

func testSessionWithRegistry() *Session {
	reg := tools.NewRegistry()
	reg.Register(tools.ToolSpec{Name: "read", Summary: "read files"})
	reg.Register(tools.ToolSpec{Name: "write", Summary: "write files"})
	reg.Register(tools.ToolSpec{Name: "skill.invoke", Summary: "invoke skill"})
	reg.Register(tools.ToolSpec{Name: "subagent.spawn", Summary: "spawn"})
	return &Session{
		Registry: reg,
		Active:   map[string]bool{"read": true, "write": true, "skill.invoke": true, "subagent.spawn": true},
	}
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}
