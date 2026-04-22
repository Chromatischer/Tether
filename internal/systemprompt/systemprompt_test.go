package systemprompt

import (
	"strings"
	"testing"
)

func TestDefaultMarkdown(t *testing.T) {
	tests := []struct {
		name     string
		template string
		want     string
	}{
		{name: "chat", template: TemplateChat, want: "You are Tether"},
		{name: "proactive", template: TemplateProactive, want: "You are Tether running in autonomous proactive mode"},
		{name: "unknown", template: "missing", want: ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := DefaultMarkdown(tc.template)
			if tc.want == "" {
				if got != "" {
					t.Fatalf("expected empty markdown for %q, got %q", tc.template, got)
				}
				return
			}
			if !strings.Contains(got, tc.want) {
				t.Fatalf("expected template %q to contain %q", tc.template, tc.want)
			}
		})
	}
}

func TestRender(t *testing.T) {
	source := "user={{.Username}} uid={{.UserID}}"
	got := Render(source, TemplateData{Username: "alice", UserID: 7})
	if got != "user=alice uid=7" {
		t.Fatalf("unexpected render output: %q", got)
	}
}

func TestRenderFallsBackToSourceOnTemplateError(t *testing.T) {
	source := "user={{.Missing}}"
	if got := Render(source, TemplateData{}); got != source {
		t.Fatalf("expected original source on execution failure, got %q", got)
	}

	source = "{{if}}"
	if got := Render(source, TemplateData{}); got != source {
		t.Fatalf("expected original source on parse failure, got %q", got)
	}
}
