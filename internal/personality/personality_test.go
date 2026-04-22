package personality

import (
	"strings"
	"testing"
)

func TestIsValidAgentKey(t *testing.T) {
	tests := []struct {
		name string
		key  string
		want bool
	}{
		{name: "chat", key: AgentChat, want: true},
		{name: "nested", key: "proactive/daily_brief", want: true},
		{name: "trimmed", key: "  custom-agent  ", want: true},
		{name: "empty", key: "   ", want: false},
		{name: "empty segment", key: "proactive//daily", want: false},
		{name: "uppercase", key: "Chat", want: false},
		{name: "spaces", key: "bad key", want: false},
		{name: "dot", key: "bad.key", want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsValidAgentKey(tc.key); got != tc.want {
				t.Fatalf("IsValidAgentKey(%q)=%v want %v", tc.key, got, tc.want)
			}
		})
	}
}

func TestNormalizeID(t *testing.T) {
	id, ok := NormalizeID("  Daily_Brief-2 ")
	if !ok {
		t.Fatal("expected valid normalized id")
	}
	if id != "daily_brief-2" {
		t.Fatalf("unexpected normalized id: %q", id)
	}

	for _, input := range []string{"", "two words", "with/slash", "bad.dot"} {
		if _, ok := NormalizeID(input); ok {
			t.Fatalf("expected invalid id for %q", input)
		}
	}
}

func TestDefaultMarkdown(t *testing.T) {
	tests := []struct {
		name     string
		agentKey string
		want     string
	}{
		{name: "chat", agentKey: AgentChat, want: "You are Tether"},
		{name: "daily brief", agentKey: AgentProactiveDailyBrief, want: "Deliver a compact daily brief"},
		{name: "open loops", agentKey: AgentProactiveOpenLoops, want: "Help the user close open loops"},
		{name: "custom proactive", agentKey: "proactive/custom", want: "Be useful in proactive mode"},
		{name: "generic", agentKey: "custom", want: "Be direct, helpful, and safe"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := DefaultMarkdown(tc.agentKey)
			if got == "" {
				t.Fatalf("expected default markdown for %q", tc.agentKey)
			}
			if !strings.Contains(got, tc.want) {
				t.Fatalf("expected %q to contain %q", tc.agentKey, tc.want)
			}
		})
	}
}
