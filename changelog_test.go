package tether

import (
	"strings"
	"testing"
)

func TestParseChangelog(t *testing.T) {
	entries := ParseChangelog("# Changelog\n\n## v0.2 (new)\n\n- two\n\n## v0.1\n\n- one\n")
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if entries[0].Version != "v0.2" || entries[1].Version != "v0.1" {
		t.Fatalf("unexpected versions: %+v", entries)
	}
}

func TestRenderChangelogFrom(t *testing.T) {
	got, err := RenderChangelogFrom("v0.4")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "## v0.4") {
		t.Fatalf("expected v0.4 section, got:\n%s", got)
	}
	if strings.Contains(got, "## v0.3") {
		t.Fatalf("did not expect older v0.3 section, got:\n%s", got)
	}
}

func TestRenderChangelogAfter(t *testing.T) {
	old := Version
	Version = "v0.5"
	t.Cleanup(func() { Version = old })

	got, ok, err := RenderChangelogAfter("v0.4")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected changelog content")
	}
	if !strings.Contains(got, "## v0.5") || strings.Contains(got, "## v0.4") {
		t.Fatalf("unexpected range:\n%s", got)
	}
}
