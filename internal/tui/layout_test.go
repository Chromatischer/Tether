package tui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"tether/internal/config"
)

func assertRenderedRect(t *testing.T, content string, wantW, wantH int) {
	t.Helper()

	lines := strings.Split(content, "\n")
	if got := len(lines); got != wantH {
		t.Fatalf("expected %d lines, got %d", wantH, got)
	}
	for i, line := range lines {
		if got := lipgloss.Width(line); got != wantW {
			t.Fatalf("line %d: expected width %d, got %d", i, wantW, got)
		}
	}
}

func TestFillAreaFillsRequestedRectangle(t *testing.T) {
	out := fillArea("x", 7, 3, colorBg)
	assertRenderedRect(t, out, 7, 3)
}

func TestFillAreaAppendsBackgroundAfterStyledContent(t *testing.T) {
	out := fillArea(styleMuted.Render("abc"), 7, 1, colorBg)
	if !strings.Contains(out, "\x1b[48;5;233m") {
		t.Fatalf("expected explicit background fill in output, got %q", out)
	}
	assertRenderedRect(t, out, 7, 1)
}

func TestMemoryViewFillsAllocatedArea(t *testing.T) {
	m := newMemoryModel().withSize(24, 6)
	assertRenderedRect(t, m.View().Content, 24, 6)
}

func TestSettingsViewFillsAllocatedArea(t *testing.T) {
	cfg := &config.Config{}
	cfg.Paths.DataDir = t.TempDir()
	m := newSettingsModel(&SessionContext{Config: cfg}).withSize(80, 7)
	assertRenderedRect(t, m.View().Content, 80, 7)
}

func TestAdminViewFillsAllocatedArea(t *testing.T) {
	cfg := &config.Config{}
	cfg.Paths.DataDir = t.TempDir()
	m := newAdminModel(&SessionContext{Config: cfg}).withSize(80, 8)
	assertRenderedRect(t, m.View().Content, 80, 8)
}
