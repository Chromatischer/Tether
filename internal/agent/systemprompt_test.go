package agent

import (
	"os"
	"strings"
	"testing"

	"tether/internal/config"
	"tether/internal/store"
	"tether/internal/systemprompt"
	"tether/internal/testutil"
	"tether/internal/userspace"
)

func TestChatSystemPromptText_RendersTemplatePlaceholders(t *testing.T) {
	cfg := &config.Config{}
	cfg.Paths.DataDir = t.TempDir()
	db := testutil.OpenTestDB(t)
	ag := &Agent{cfg: cfg, db: db}

	u, err := store.CreateUser(db, "alice", "password")
	if err != nil {
		t.Fatal(err)
	}
	d := userspace.ForUser(cfg.Paths.DataDir, u.ID)
	if err := userspace.Ensure(d); err != nil {
		t.Fatal(err)
	}
	p, ok := userspace.PromptTemplateAbsPath(d, systemprompt.TemplateChat)
	if !ok {
		t.Fatal("expected chat system prompt path")
	}
	if err := os.MkdirAll(userspace.ForUser(cfg.Paths.DataDir, u.ID).Config, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(strings.TrimSuffix(p, "/chat_system.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("user={{.Username}} uid={{.UserID}} conv={{.ConversationID}} session={{.SessionID}} mode={{.Mode}}"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := ag.chatSystemPromptText(u.ID, 99)
	wantParts := []string{"user=alice", "uid=1", "conv=99", "session=99", "mode=chat"}
	for _, part := range wantParts {
		if !strings.Contains(got, part) {
			t.Fatalf("expected rendered prompt to contain %q, got %q", part, got)
		}
	}
}
