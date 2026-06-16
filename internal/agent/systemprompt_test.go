package agent

import (
	"strings"
	"testing"

	"tether/internal/config"
	"tether/internal/store"
	"tether/internal/testutil"
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

	// The system prompt is global (embedded), but template data is still
	// rendered per call: the username and mode placeholders resolve.
	got := ag.chatSystemPromptText(u.ID, 99)
	for _, part := range []string{"alice", "current mode is chat"} {
		if !strings.Contains(got, part) {
			t.Fatalf("expected rendered prompt to contain %q, got %q", part, got)
		}
	}
}
