package term

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

var (
	mdHeadPattern     = regexp.MustCompile(`^(#{1,6})\s+(.+)$`)
	mdRulePattern     = regexp.MustCompile(`^\s{0,3}((\*\s*){3,}|(-\s*){3,}|(_\s*){3,})\s*$`)
	mdListPattern     = regexp.MustCompile(`^(\s*)([-*+]|\d+\.)\s+(.+)$`)
	mdTableSepPattern = regexp.MustCompile(`^:?-{3,}:?$`)
	mdBlockquotePat   = regexp.MustCompile(`^>\s?(.*)$`)
	mdLinkPattern     = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)
	mdBoldStarPat     = regexp.MustCompile(`\*\*(.+?)\*\*`)
	mdBoldUndPat      = regexp.MustCompile(`__(.+?)__`)
	mdItalicStarPat   = regexp.MustCompile(`\*([^*\n]+)\*`)
	mdItalicUndPat    = regexp.MustCompile(`_([^_\n]+)_`)
	mdInlineCodePat   = regexp.MustCompile("`([^`\n]+)`")
	mdTodoPat         = regexp.MustCompile(`^(\s*)([-*+])\s+\[( |x|X)\]\s(.+)$`)
)

func renderMarkdown(text string, width int, profile TerminalProfile) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	if width <= 0 {
		width = 80
	}

	lines := strings.Split(text, "\n")
	pal := newPalette(profile.ColorLevel)

	var sb strings.Builder
	for i := 0; i < len(lines); {
		line := lines[i]
		trimmed := strings.TrimSpace(line)

		if trimmed == "" {
			sb.WriteByte('\n')
			i++
			continue
		}

		if next, ok := renderCodeBlock(lines, i, &sb, pal, profile); ok {
			i = next
			continue
		}

		if next, ok := renderTable(lines, i, &sb, pal, profile, width); ok {
			i = next
			continue
		}

		rendered := renderLine(line, pal, profile, width)
		for _, wrappedLine := range strings.Split(rendered, "\n") {
			sb.WriteString(wrappedLine)
			sb.WriteByte('\n')
		}
		i++
	}

	return strings.TrimRight(sb.String(), "\n")
}

func renderLine(line string, pal palette, profile TerminalProfile, width int) string {
	if profile.ColorLevel == ColorNone {
		return wrapPlain(line, width)
	}

	if mdRulePattern.MatchString(line) {
		rule := strings.Repeat("─", min(width, 60))
		return apply(pal.muted(), rule)
	}

	if m := mdHeadPattern.FindStringSubmatch(line); m != nil {
		level := len(m[1])
		text := m[2]
		rendered := renderInline(text, pal, profile)
		if level <= 2 {
			return apply(pal.brightWhite(), bold(rendered))
		}
		return apply(pal.body(), bold(rendered))
	}

	if m := mdBlockquotePat.FindStringSubmatch(line); m != nil {
		inner := m[1]
		rendered := renderInline(inner, pal, profile)
		prefix := apply(pal.muted(), "│ ")
		return wrapStyled(prefix+rendered, width)
	}

	if m := mdListPattern.FindStringSubmatch(line); m != nil {
		indent := m[1]
		marker := m[2]
		content := m[3]
		rendered := renderInline(content, pal, profile)
		styledMarker := apply(pal.amber(), marker)
		return indent + styledMarker + " " + wrapIndented(rendered, width, len(indent)+2)
	}

	if m := mdTodoPat.FindStringSubmatch(line); m != nil {
		indent := m[1]
		marker := m[2]
		checked := strings.ToLower(m[3]) == "x"
		content := m[4]
		rendered := renderInline(content, pal, profile)
		box := "[ ]"
		if checked {
			box = "[x]"
		}
		styledBox := apply(pal.amber(), box)
		return indent + marker + " " + styledBox + " " + wrapIndented(rendered, width, len(indent)+4)
	}

	rendered := renderInline(line, pal, profile)
	return wrapStyled(rendered, width)
}

func renderInline(text string, pal palette, profile TerminalProfile) string {
	if profile.ColorLevel == ColorNone {
		return text
	}
	return renderInlineColored(text, pal)
}

func renderInlineColored(text string, pal palette) string {
	text = mdBoldStarPat.ReplaceAllStringFunc(text, func(m string) string {
		inner := mdBoldStarPat.FindStringSubmatch(m)[1]
		return bold(renderInlineCode(inner, pal))
	})
	text = mdBoldUndPat.ReplaceAllStringFunc(text, func(m string) string {
		inner := mdBoldUndPat.FindStringSubmatch(m)[1]
		return bold(renderInlineCode(inner, pal))
	})
	text = mdItalicStarPat.ReplaceAllStringFunc(text, func(m string) string {
		inner := mdItalicStarPat.FindStringSubmatch(m)[1]
		return italic(renderInlineCode(inner, pal))
	})
	text = mdItalicUndPat.ReplaceAllStringFunc(text, func(m string) string {
		inner := mdItalicUndPat.FindStringSubmatch(m)[1]
		return italic(renderInlineCode(inner, pal))
	})
	text = mdLinkPattern.ReplaceAllStringFunc(text, func(m string) string {
		parts := mdLinkPattern.FindStringSubmatch(m)
		label := parts[1]
		return apply(pal.yellow(), underline(label))
	})
	text = renderInlineCode(text, pal)
	return text
}

func renderInlineCode(text string, pal palette) string {
	return mdInlineCodePat.ReplaceAllStringFunc(text, func(m string) string {
		inner := mdInlineCodePat.FindStringSubmatch(m)[1]
		return dim(reverse(inner))
	})
}

func renderCodeBlock(lines []string, start int, sb *strings.Builder, pal palette, profile TerminalProfile) (int, bool) {
	line := strings.TrimLeft(lines[start], " ")
	if !strings.HasPrefix(line, "```") {
		return start, false
	}

	end := start + 1
	for end < len(lines) {
		if strings.HasPrefix(strings.TrimLeft(lines[end], " "), "```") {
			end++
			break
		}
		end++
	}

	for j := start + 1; j < end-1; j++ {
		codeLine := lines[j]
		if profile.ColorLevel > ColorNone {
			sb.WriteString(apply(pal.muted(), "  │ "))
			sb.WriteString(dim(codeLine))
		} else {
			sb.WriteString("  │ ")
			sb.WriteString(codeLine)
		}
		sb.WriteByte('\n')
	}

	if profile.ColorLevel > ColorNone {
		sb.WriteString(apply(pal.muted(), "  ──"))
	} else {
		sb.WriteString("  --")
	}
	sb.WriteByte('\n')

	return end, true
}

func renderTable(lines []string, start int, sb *strings.Builder, pal palette, profile TerminalProfile, width int) (int, bool) {
	firstRow := strings.Split(lines[start], "|")
	if len(firstRow) < 2 {
		return 0, false
	}

	if start+1 >= len(lines) {
		return 0, false
	}

	if !mdTableSepPattern.MatchString(strings.TrimSpace(strings.ReplaceAll(lines[start+1], "|", ""))) {
		return 0, false
	}

	cols := len(firstRow) - 2
	if cols < 1 {
		return 0, false
	}

	end := start + 2
	for end < len(lines) {
		rowCols := strings.Split(lines[end], "|")
		if len(rowCols) < 2 {
			break
		}
		end++
	}

	if profile.ColorLevel > ColorNone {
		for r := start; r < end; r++ {
			cells := strings.Split(lines[r], "|")[1:]

			sb.WriteString(apply(pal.muted(), "│ "))
			for c := 0; c < cols && c < len(cells); c++ {
				cell := strings.TrimSpace(cells[c])
				if r == start {
					sb.WriteString(bold(renderInline(cell, pal, profile)))
				} else if mdTableSepPattern.MatchString(cell) {
					sb.WriteString(apply(pal.muted(), strings.Repeat("─", 8)))
				} else {
					sb.WriteString(renderInline(cell, pal, profile))
				}
				if c < cols-1 {
					sb.WriteString(apply(pal.muted(), " │ "))
				}
			}
			sb.WriteString(apply(pal.muted(), " │"))
			sb.WriteByte('\n')
		}
	} else {
		for r := start; r < end; r++ {
			cells := strings.Split(lines[r], "|")[1:]
			sb.WriteString("| ")
			for c := 0; c < cols && c < len(cells); c++ {
				cell := strings.TrimSpace(cells[c])
				sb.WriteString(cell)
				if c < cols-1 {
					sb.WriteString(" | ")
				}
			}
			sb.WriteString(" |")
			sb.WriteByte('\n')
		}
	}

	return end, true
}

func wrapPlain(text string, width int) string {
	if width <= 0 || visualWidthANSI(text) <= width {
		return text
	}
	return wrapAtWordsANSI(text, width)
}

func wrapStyled(text string, width int) string {
	if width <= 0 {
		return text
	}
	clean := ansiStripPattern.ReplaceAllString(text, "")
	if utf8.RuneCountInString(clean) <= width {
		return text
	}
	return wrapAtWordsANSI(text, width)
}

func wrapIndented(text string, width, indent int) string {
	if width <= indent {
		return text
	}
	wrapWidth := width - indent
	if wrapWidth <= 0 {
		return text
	}
	clean := ansiStripPattern.ReplaceAllString(text, "")
	if utf8.RuneCountInString(clean) <= wrapWidth {
		return text
	}
	wrapped := wrapAtWordsANSI(text, wrapWidth)
	indentStr := strings.Repeat(" ", indent)
	lines := strings.Split(wrapped, "\n")
	for i := 1; i < len(lines); i++ {
		lines[i] = indentStr + lines[i]
	}
	return strings.Join(lines, "\n")
}

func wrapAtWordsANSI(text string, maxWidth int) string {
	var out strings.Builder
	words := splitWords(text)
	lineWidth := 0
	lineStart := true

	for _, word := range words {
		w := utf8.RuneCountInString(ansiStripPattern.ReplaceAllString(word, ""))
		if w == 0 {
			continue
		}

		if !lineStart && lineWidth+w+1 > maxWidth {
			out.WriteByte('\n')
			lineWidth = 0
			lineStart = true
		}

		if lineStart && w > maxWidth {
			wrapLongWord(&out, word, maxWidth, &lineWidth, &lineStart)
			continue
		}

		if !lineStart {
			out.WriteByte(' ')
			lineWidth++
		}
		out.WriteString(word)
		lineWidth += w
		lineStart = false
	}
	return out.String()
}

func wrapLongWord(out *strings.Builder, word string, maxWidth int, lineWidth *int, lineStart *bool) {
	*lineWidth = 0
	*lineStart = false
	i := 0
	firstChunk := true
	for i < len(word) {
		if word[i] == '\x1b' {
			end := strings.IndexByte(word[i:], 'm')
			if end < 0 {
				out.WriteByte(word[i])
				i++
				continue
			}
			out.WriteString(word[i : i+end+1])
			i += end + 1
			continue
		}
		r, sz := utf8.DecodeRuneInString(word[i:])
		if r < 32 {
			i += sz
			continue
		}
		if !firstChunk && *lineWidth >= maxWidth {
			out.WriteByte('\n')
			*lineWidth = 0
		}
		out.WriteString(word[i : i+sz])
		*lineWidth++
		i += sz
		firstChunk = false
	}
}

func splitWords(text string) []string {
	var words []string
	var current strings.Builder
	insideANSI := false

	for _, r := range text {
		if r == '\x1b' {
			insideANSI = true
			current.WriteRune(r)
			continue
		}
		if insideANSI {
			current.WriteRune(r)
			if r == 'm' {
				insideANSI = false
			}
			continue
		}
		if r == ' ' {
			if current.Len() > 0 {
				words = append(words, current.String())
				current.Reset()
			}
			words = append(words, " ")
			continue
		}
		current.WriteRune(r)
	}
	if current.Len() > 0 {
		words = append(words, current.String())
	}
	return words
}

func visualWidthANSI(text string) int {
	return utf8.RuneCountInString(ansiStripPattern.ReplaceAllString(text, ""))
}
