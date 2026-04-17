package agent

import (
	"testing"

	"tether/internal/agent/toolset"
	"tether/internal/config"
	"tether/internal/store"
	"tether/internal/testutil"
)

func TestBuildContextInputItems_SkipsToolCallMessages(t *testing.T) {
	ag := &Agent{
		cfg:      &config.Config{},
		db:       testutil.OpenTestDB(t),
		sessions: map[int64]*toolset.Session{},
	}
	history := []store.Message{
		{ID: 1, Role: "user", Content: "hello"},
		{ID: 2, Role: "tool_call", Content: "bash {\"command\":\"ls\"}"},
		{ID: 3, Role: "assistant", Content: "done"},
	}

	items, err := ag.buildContextInputItems(1, 1, history)
	if err != nil {
		t.Fatal(err)
	}

	for _, it := range items {
		if it.Type != "message" {
			continue
		}
		for _, p := range it.Content {
			if p.Text == "bash {\"command\":\"ls\"}" {
				t.Fatalf("tool_call content should not be included in model context")
			}
		}
	}
}
