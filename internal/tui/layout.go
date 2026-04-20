package tui

import (
	"image/color"
	"strings"

	"charm.land/bubbles/v2/viewport"
	"charm.land/lipgloss/v2"
)

func fillArea(content string, w, h int, bg color.Color) string {
	if w <= 0 {
		return content
	}

	bgStyle := lipgloss.NewStyle().Background(bg)
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		if gap := w - lipgloss.Width(line); gap > 0 {
			line += bgStyle.Width(gap).Render("")
		}
		lines[i] = line
	}

	if h > 0 && len(lines) < h {
		blank := bgStyle.Width(w).Render("")
		for len(lines) < h {
			lines = append(lines, blank)
		}
	}
	return strings.Join(lines, "\n")
}

func setViewportContent(vp *viewport.Model, content string, bg color.Color) {
	if vp == nil {
		return
	}
	lines := strings.Split(content, "\n")
	if vp.Width() > 0 {
		bgStyle := lipgloss.NewStyle().Background(bg)
		for i, line := range lines {
			if gap := vp.Width() - lipgloss.Width(line); gap > 0 {
				line += bgStyle.Width(gap).Render("")
			}
			lines[i] = line
		}
	}
	vp.SetContentLines(lines)
}
