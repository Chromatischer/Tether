package tui

import (
	"image/color"
	"regexp"
	"strings"

	"charm.land/lipgloss/v2"
	liptable "charm.land/lipgloss/v2/table"
)

type richTextVariant string

const (
	richTextAssistant richTextVariant = "assistant"
	richTextSystem    richTextVariant = "system"
)

var (
	ansiEscapePattern = regexp.MustCompile(`\x1b\[[0-9;]*m`)
	headingPattern    = regexp.MustCompile(`^(#{1,6})\s+(.+)$`)
	rulePattern       = regexp.MustCompile(`^\s{0,3}((\*\s*){3,}|(-\s*){3,}|(_\s*){3,})\s*$`)
	listPattern       = regexp.MustCompile(`^(\s*)([-*+]|\d+\.)\s+(.+)$`)
	linkPattern       = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)
	boldStarPattern   = regexp.MustCompile(`\*\*(.+?)\*\*`)
	boldUndersPattern = regexp.MustCompile(`__(.+?)__`)
	italicStarPattern = regexp.MustCompile(`\*([^*\n]+)\*`)
	italicUndPattern  = regexp.MustCompile(`_([^_\n]+)_`)
	tableSepPattern   = regexp.MustCompile(`^:?-{3,}:?$`)
)

func renderRichText(text string, width int, variant richTextVariant) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	if width <= 0 {
		width = 40
	}

	lines := strings.Split(text, "\n")
	blocks := make([]string, 0, len(lines))
	for i := 0; i < len(lines); {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			i++
			continue
		}
		if next, out, ok := renderTableBlock(lines, i, width, variant); ok {
			blocks = append(blocks, out)
			i = next
			continue
		}
		if next, out, ok := renderCodeFenceBlock(lines, i, width, variant); ok {
			blocks = append(blocks, out)
			i = next
			continue
		}
		if rulePattern.MatchString(trimmed) {
			blocks = append(blocks, richRuleStyle(variant).Render(strings.Repeat("─", max(8, width))))
			i++
			continue
		}
		if m := headingPattern.FindStringSubmatch(trimmed); m != nil {
			level := len(m[1])
			content := lipgloss.Wrap(renderInline(m[2], variant), width, " ")
			blocks = append(blocks, richHeadingStyle(variant, level).Render(content))
			i++
			continue
		}
		if m := listPattern.FindStringSubmatch(line); m != nil {
			indent := len(m[1])
			prefix := strings.Repeat(" ", indent) + m[2] + " "
			bodyWidth := max(8, width-lipgloss.Width(prefix))
			body := lipgloss.Wrap(renderInline(m[3], variant), bodyWidth, " ")
			blocks = append(blocks, indentWrapped(body, prefix))
			i++
			continue
		}

		para := []string{trimmed}
		j := i + 1
		for ; j < len(lines); j++ {
			next := strings.TrimSpace(lines[j])
			if next == "" || headingPattern.MatchString(next) || rulePattern.MatchString(next) || listPattern.MatchString(lines[j]) {
				break
			}
			if _, _, ok := renderTableBlock(lines, j, width, variant); ok {
				break
			}
			if _, _, ok := renderCodeFenceBlock(lines, j, width, variant); ok {
				break
			}
			para = append(para, next)
		}
		joined := strings.Join(para, " ")
		blocks = append(blocks, lipgloss.Wrap(renderInline(joined, variant), width, " "))
		i = j
	}

	return strings.Join(blocks, "\n\n")
}

func RenderAssistantRichText(text string, width int) string {
	return renderRichText(text, width, richTextAssistant)
}

func RenderSystemRichText(text string, width int) string {
	return renderRichText(text, width, richTextSystem)
}

func RenderAssistantTable(headers []string, rows [][]string, width int, styleFunc func(row, col int, value string) lipgloss.Style) string {
	if width <= 0 {
		width = 40
	}
	t := liptable.New().
		Headers(headers...).
		Rows(rows...).
		Width(width).
		Border(lipgloss.RoundedBorder()).
		BorderStyle(richTableBorderStyle(richTextAssistant)).
		StyleFunc(func(row, col int) lipgloss.Style {
			if row == liptable.HeaderRow {
				return richTableHeaderStyle(richTextAssistant)
			}
			if styleFunc != nil && row >= 0 && row < len(rows) && col >= 0 && col < len(rows[row]) {
				return styleFunc(row, col, rows[row][col])
			}
			return richTableCellStyle(richTextAssistant)
		})
	return t.String()
}

func renderCodeFenceBlock(lines []string, start, width int, variant richTextVariant) (int, string, bool) {
	if start >= len(lines) {
		return start, "", false
	}
	first := strings.TrimSpace(lines[start])
	if !strings.HasPrefix(first, "```") {
		return start, "", false
	}
	body := make([]string, 0, 8)
	i := start + 1
	for ; i < len(lines); i++ {
		if strings.HasPrefix(strings.TrimSpace(lines[i]), "```") {
			i++
			break
		}
		body = append(body, lines[i])
	}
	content := strings.Join(body, "\n")
	content = lipgloss.Wrap(content, width, "")
	return i, richCodeBlockStyle(variant).MaxWidth(width).Render(content), true
}

func renderTableBlock(lines []string, start, width int, variant richTextVariant) (int, string, bool) {
	if start+1 >= len(lines) {
		return start, "", false
	}
	header, ok := parseMarkdownTableRow(lines[start])
	if !ok {
		return start, "", false
	}
	sep, ok := parseMarkdownTableRow(lines[start+1])
	if !ok || len(sep) != len(header) {
		return start, "", false
	}
	for _, cell := range sep {
		if !tableSepPattern.MatchString(strings.ReplaceAll(cell, " ", "")) {
			return start, "", false
		}
	}

	rows := make([][]string, 0, 4)
	i := start + 2
	for ; i < len(lines); i++ {
		row, ok := parseMarkdownTableRow(lines[i])
		if !ok || len(row) != len(header) {
			break
		}
		for j := range row {
			row[j] = renderInline(row[j], variant)
		}
		rows = append(rows, row)
	}
	for j := range header {
		header[j] = renderInline(header[j], variant)
	}

	t := liptable.New().
		Headers(header...).
		Rows(rows...).
		Width(width).
		Border(lipgloss.RoundedBorder()).
		BorderStyle(richTableBorderStyle(variant)).
		StyleFunc(func(row, col int) lipgloss.Style {
			if row == liptable.HeaderRow {
				return richTableHeaderStyle(variant)
			}
			return richTableCellStyle(variant)
		})
	return i, t.String(), true
}

func parseMarkdownTableRow(line string) ([]string, bool) {
	line = strings.TrimSpace(line)
	if !strings.Contains(line, "|") {
		return nil, false
	}
	line = strings.TrimPrefix(line, "|")
	line = strings.TrimSuffix(line, "|")
	parts := strings.Split(line, "|")
	if len(parts) < 2 {
		return nil, false
	}
	row := make([]string, 0, len(parts))
	for _, part := range parts {
		row = append(row, strings.TrimSpace(part))
	}
	return row, true
}

func renderInline(text string, variant richTextVariant) string {
	if text == "" {
		return ""
	}
	segments := strings.Split(text, "`")
	for i := range segments {
		if i%2 == 1 {
			segments[i] = richCodeStyle(variant).Render(segments[i])
			continue
		}
		segments[i] = renderInlineNoCode(segments[i], variant)
	}
	return strings.Join(segments, "")
}

func renderInlineNoCode(text string, variant richTextVariant) string {
	text = linkPattern.ReplaceAllStringFunc(text, func(m string) string {
		sub := linkPattern.FindStringSubmatch(m)
		if len(sub) != 3 {
			return m
		}
		return richLinkStyle(variant).Render(sub[1]) + richMutedStyle(variant).Render(" ("+sub[2]+")")
	})
	text = boldStarPattern.ReplaceAllStringFunc(text, func(m string) string {
		sub := boldStarPattern.FindStringSubmatch(m)
		if len(sub) != 2 {
			return m
		}
		return richStrongStyle(variant).Render(sub[1])
	})
	text = boldUndersPattern.ReplaceAllStringFunc(text, func(m string) string {
		sub := boldUndersPattern.FindStringSubmatch(m)
		if len(sub) != 2 {
			return m
		}
		return richStrongStyle(variant).Render(sub[1])
	})
	text = italicStarPattern.ReplaceAllStringFunc(text, func(m string) string {
		sub := italicStarPattern.FindStringSubmatch(m)
		if len(sub) != 2 {
			return m
		}
		return richEmphStyle(variant).Render(sub[1])
	})
	text = italicUndPattern.ReplaceAllStringFunc(text, func(m string) string {
		sub := italicUndPattern.FindStringSubmatch(m)
		if len(sub) != 2 {
			return m
		}
		return richEmphStyle(variant).Render(sub[1])
	})
	return text
}

func indentWrapped(body string, prefix string) string {
	lines := strings.Split(body, "\n")
	for i := range lines {
		if i == 0 {
			lines[i] = prefix + lines[i]
		} else {
			lines[i] = strings.Repeat(" ", lipgloss.Width(prefix)) + lines[i]
		}
	}
	return strings.Join(lines, "\n")
}

func richBodyColor(variant richTextVariant) color.Color {
	if variant == richTextSystem {
		return colorMuted
	}
	return colorBody
}

func richColor(assistant, system string, variant richTextVariant) color.Color {
	if variant == richTextSystem {
		return lipgloss.Color(system)
	}
	return lipgloss.Color(assistant)
}

func richStrongStyle(variant richTextVariant) lipgloss.Style {
	return lipgloss.NewStyle().Bold(true).Foreground(richColor("255", "194", variant))
}

func richEmphStyle(variant richTextVariant) lipgloss.Style {
	return lipgloss.NewStyle().Italic(true).Foreground(richColor("252", "223", variant))
}

func richHeadingStyle(variant richTextVariant, level int) lipgloss.Style {
	switch level {
	case 1:
		return lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("255"))
	case 2:
		return lipgloss.NewStyle().Bold(true).Foreground(colorBody)
	default:
		return lipgloss.NewStyle().Bold(true).Foreground(colorBody)
	}
}

func richRuleStyle(variant richTextVariant) lipgloss.Style {
	return lipgloss.NewStyle().Foreground(richColor("179", "244", variant))
}

func richLinkStyle(variant richTextVariant) lipgloss.Style {
	return lipgloss.NewStyle().Underline(true).Foreground(richColor("179", "223", variant))
}

func richMutedStyle(variant richTextVariant) lipgloss.Style {
	return lipgloss.NewStyle().Foreground(colorMuted)
}

func richCodeStyle(variant richTextVariant) lipgloss.Style {
	return lipgloss.NewStyle().
		Foreground(richColor("230", "255", variant)).
		Background(colorToolBorder).
		Padding(0, 1)
}

func richCodeBlockStyle(variant richTextVariant) lipgloss.Style {
	return lipgloss.NewStyle().
		Foreground(richBodyColor(variant)).
		Background(colorToolBorder). // "236"
		BorderLeft(true).
		BorderStyle(lipgloss.ThickBorder()).
		BorderForeground(colorAmber).
		Padding(0, 1)
}

func richTableHeaderStyle(variant richTextVariant) lipgloss.Style {
	return lipgloss.NewStyle().Bold(true).Foreground(richColor("179", "223", variant))
}

func richTableCellStyle(variant richTextVariant) lipgloss.Style {
	return lipgloss.NewStyle().Foreground(richBodyColor(variant))
}

func richTableBorderStyle(variant richTextVariant) lipgloss.Style {
	return lipgloss.NewStyle().Foreground(colorToolBorder)
}

func stripANSI(text string) string {
	return ansiEscapePattern.ReplaceAllString(text, "")
}
