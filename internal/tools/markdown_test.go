package tools

import (
	"strings"
	"testing"
)

func TestRenderToolsMarkdownSortsAndIncludesSections(t *testing.T) {
	doc := RenderToolsMarkdown([]ToolSpec{
		{
			Name:        "zeta.run",
			Summary:     "Run zeta.",
			WhenToUse:   "Use when zeta is needed.",
			Safety:      "Confirm before destructive actions.",
			InputSchema: map[string]any{"type": "object"},
			OutputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{"ok": map[string]any{"type": "boolean"}},
			},
			Examples: []ToolExample{{
				Title:  "Basic run",
				Args:   map[string]any{"path": "workspace"},
				Result: map[string]any{"ok": true},
				Notes:  "Runs the tool.",
			}},
		},
		{
			Name:        "alpha.check",
			Summary:     "Check alpha.",
			InputSchema: map[string]any{"type": "object"},
		},
	})

	if first := strings.Index(doc, "## `alpha.check`"); first < 0 {
		t.Fatalf("expected alpha section in document: %q", doc)
	} else if second := strings.Index(doc, "## `zeta.run`"); second < first {
		t.Fatalf("expected tools to be sorted alphabetically: %q", doc)
	}

	for _, want := range []string{
		"# Tether tool reference",
		"- [`alpha.check`](#alphacheck) — Check alpha.",
		"**When to use**",
		"**Safety / confirmation**",
		"### Output shape",
		"### Example",
		"\"ok\": true",
	} {
		if !strings.Contains(doc, want) {
			t.Fatalf("expected markdown to contain %q", want)
		}
	}
}

func TestSchemaShapeAndLLMDescription(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name":  map[string]any{"type": "string"},
			"tags":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"count": map[string]any{"type": "integer"},
		},
		"required": []any{"name"},
	}
	if got := SchemaShape(schema); got != "{count?:integer, name:string, tags?:[string]}" {
		t.Fatalf("unexpected schema shape: %q", got)
	}

	spec := ToolSpec{
		Name:         "tool.run",
		Summary:      "Run the tool.",
		Safety:       "Confirm first.\nExtra details.",
		InputSchema:  schema,
		OutputSchema: map[string]any{"enum": []any{"ok", "error"}},
	}
	desc := LLMDescription(spec)
	for _, want := range []string{
		"Run the tool.",
		"Inputs: {count?:integer, name:string, tags?:[string]}",
		"Returns: ok | error",
		"Safety: Confirm first.",
	} {
		if !strings.Contains(desc, want) {
			t.Fatalf("expected description to contain %q in %q", want, desc)
		}
	}
}

func TestPrettyJSONAndAnchor(t *testing.T) {
	if got := prettyJSON(nil); got != "null" {
		t.Fatalf("prettyJSON(nil)=%q", got)
	}
	if got := anchor(" Tool/Name.v1_test `x` "); got != "toolnamev1-test-x" {
		t.Fatalf("unexpected anchor: %q", got)
	}
}
