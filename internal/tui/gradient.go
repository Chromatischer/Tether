package tui

import (
	"image/color"
	"strings"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
)

func gradientTextBlock(text string, bg color.Color, start, end color.Color) string {
	runes := []rune(text)
	visible := 0
	for _, r := range runes {
		if r != '\n' {
			visible++
		}
	}
	if visible == 0 {
		return text
	}
	gradient := lipgloss.Blend1D(visible, start, end)
	var b strings.Builder
	index := 0
	for _, r := range runes {
		if r == '\n' {
			b.WriteRune(r)
			continue
		}
		b.WriteString(lipgloss.NewStyle().
			Foreground(gradient[index]).
			Background(bg).
			Render(string(r)))
		index++
	}
	return b.String()
}

func renderBrandWordmark(text string, bg color.Color, gradientsEnabled bool) string {
	if !gradientsEnabled {
		return lipgloss.NewStyle().
			Foreground(colorAmber).
			Background(bg).
			Bold(true).
			Render(strings.ToUpper(text))
	}
	return renderColumnGradientText(
		strings.ToUpper(text),
		bg,
		6,
		generateGradientStops(
			lipgloss.Color("#cf6a32"),
			lipgloss.Color("#e08a1b"),
			4,
		)...,
	)
}

func renderBrandLogo(text string, bg color.Color, gradientsEnabled bool) string {
	if !gradientsEnabled {
		return text
	}
	return renderGridGradientText(
		text,
		bg,
		24,
		18,
		18,
		generateGradientStops(
			lipgloss.Color("#b94f2b"),
			lipgloss.Color("#e59617"),
			10,
		)...,
	)
}

func generateGradientStops(start, end color.Color, count int) []color.Color {
	count = max(2, count)
	return lipgloss.Blend1D(count, start, end)
}

func renderColumnGradientText(text string, bg color.Color, oversample int, stops ...color.Color) string {
	lines := strings.Split(strings.ToUpper(text), "\n")
	maxWidth := 0
	for _, line := range lines {
		maxWidth = max(maxWidth, utf8.RuneCountInString(line))
	}
	if maxWidth == 0 {
		return text
	}

	// Oversample heavily so each displayed column lands on a very smooth
	// truecolor ramp instead of visibly stepping from rune to rune.
	gradientSteps := max(maxWidth*max(1, oversample), maxWidth)
	gradient := lipgloss.Blend1D(gradientSteps, stops...)

	var out strings.Builder
	for lineIndex, line := range lines {
		if lineIndex > 0 {
			out.WriteRune('\n')
		}
		col := 0
		for _, r := range line {
			cell := string(r)
			gradientIndex := min((col*gradientSteps)/max(1, maxWidth), len(gradient)-1)
			if r == ' ' {
				out.WriteString(lipgloss.NewStyle().
					Background(bg).
					Render(cell))
				col++
				continue
			}
			out.WriteString(lipgloss.NewStyle().
				Foreground(gradient[gradientIndex]).
				Background(bg).
				Bold(true).
				Render(cell))
			col++
		}
	}
	return out.String()
}

func renderGridGradientText(text string, bg color.Color, xOversample int, yOversample int, angle float64, stops ...color.Color) string {
	lines := strings.Split(text, "\n")
	maxWidth := 0
	for _, line := range lines {
		maxWidth = max(maxWidth, utf8.RuneCountInString(line))
	}
	height := len(lines)
	if maxWidth == 0 || height == 0 {
		return text
	}

	gridW := max(maxWidth*max(1, xOversample), maxWidth)
	gridH := max(height*max(1, yOversample), height)
	gradient := lipgloss.Blend2D(gridW, gridH, angle, stops...)

	var out strings.Builder
	for y, line := range lines {
		if y > 0 {
			out.WriteRune('\n')
		}
		x := 0
		for _, r := range line {
			cell := string(r)
			if r == ' ' {
				out.WriteString(lipgloss.NewStyle().
					Background(bg).
					Render(cell))
				x++
				continue
			}
			// Terminals only allow one foreground/background color per cell. For
			// dense block glyphs, render a half-block so each cell can carry two
			// sampled shades and the logo reads smoother than a single flat color.
			if r == '█' {
				leftX := min((x*gridW)/max(1, maxWidth), gridW-1)
				rightX := min(((x+1)*gridW)/max(1, maxWidth)-1, gridW-1)
				sampleY := min((y*gridH)/max(1, height), gridH-1)
				fg := gradient[sampleY*gridW+leftX]
				bg2 := gradient[sampleY*gridW+rightX]
				out.WriteString(lipgloss.NewStyle().
					Foreground(fg).
					Background(bg2).
					Render("▌"))
				x++
				continue
			}
			sampleX := min((x*gridW)/max(1, maxWidth), gridW-1)
			sampleY := min((y*gridH)/max(1, height), gridH-1)
			out.WriteString(lipgloss.NewStyle().
				Foreground(gradient[sampleY*gridW+sampleX]).
				Background(bg).
				Bold(true).
				Render(cell))
			x++
		}
	}
	return out.String()
}
