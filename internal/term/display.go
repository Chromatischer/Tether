package term

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"tether/internal/tui"
)

var (
	resetCode     = "\033[0m"
	boldCode      = "\033[1m"
	dimCode       = "\033[2m"
	italicCode    = "\033[3m"
	underlineCode = "\033[4m"
	reverseCode   = "\033[7m"
)

func color256(n int) string   { return fmt.Sprintf("\033[38;5;%dm", n) }
func colorTrue(r, g, b int) string { return fmt.Sprintf("\033[38;2;%d;%d;%dm", r, g, b) }

type ansiStyle func(string) string

func withANSI(code string) ansiStyle {
	return func(s string) string {
		if s == "" { return s }
		return code + s + resetCode
	}
}

func bold(s string) string     { return withANSI(boldCode)(s) }
func dim(s string) string      { return withANSI(dimCode)(s) }
func italic(s string) string   { return withANSI(italicCode)(s) }
func underline(s string) string { return withANSI(underlineCode)(s) }
func reverse(s string) string  { return withANSI(reverseCode)(s) }

func colorFg256(n int) ansiStyle          { return withANSI(color256(n)) }
func colorFgTrue(r, g, b int) ansiStyle { return withANSI(colorTrue(r, g, b)) }

type palette struct{ Level ColorLevel }

func newPalette(level ColorLevel) palette { return palette{Level: level} }

func (p palette) body() ansiStyle {
	switch {
	case p.Level >= ColorTrue:  return colorFgTrue(212, 212, 212)
	case p.Level >= Color256:   return colorFg256(252)
	case p.Level >= Color16:    return withANSI("\033[37m")
	default:                    return func(s string) string { return s }
	}
}

func (p palette) bodyCode() string {
	switch {
	case p.Level >= ColorTrue:  return colorTrue(212, 212, 212)
	case p.Level >= Color256:   return color256(252)
	case p.Level >= Color16:    return "\033[37m"
	default:                    return ""
	}
}

func (p palette) mutedCode() string {
	switch {
	case p.Level >= ColorTrue:  return colorTrue(128, 128, 128)
	case p.Level >= Color256:   return color256(244)
	case p.Level >= Color16:    return "\033[90m"
	default:                    return ""
	}
}
func (p palette) muted() ansiStyle {
	switch {
	case p.Level >= ColorTrue:  return colorFgTrue(128, 128, 128)
	case p.Level >= Color256:   return colorFg256(244)
	case p.Level >= Color16:    return withANSI("\033[90m")
	default:                    return func(s string) string { return s }
	}
}
func (p palette) cyan() ansiStyle {
	switch {
	case p.Level >= ColorTrue:  return colorFgTrue(80, 200, 200)
	case p.Level >= Color256:   return colorFg256(51)
	case p.Level >= Color16:    return withANSI("\033[36m")
	default:                    return func(s string) string { return s }
	}
}
func (p palette) yellow() ansiStyle {
	switch {
	case p.Level >= ColorTrue:  return colorFgTrue(220, 200, 80)
	case p.Level >= Color256:   return colorFg256(220)
	case p.Level >= Color16:    return withANSI("\033[33m")
	default:                    return func(s string) string { return s }
	}
}
func (p palette) amber() ansiStyle {
	switch {
	case p.Level >= ColorTrue:  return colorFgTrue(229, 150, 23)
	case p.Level >= Color256:   return colorFg256(172)
	case p.Level >= Color16:    return withANSI("\033[33m")
	default:                    return func(s string) string { return s }
	}
}
func (p palette) red() ansiStyle {
	switch {
	case p.Level >= ColorTrue:  return colorFgTrue(255, 80, 80)
	case p.Level >= Color256:   return colorFg256(203)
	case p.Level >= Color16:    return withANSI("\033[31m")
	default:                    return func(s string) string { return s }
	}
}
func (p palette) brightWhite() ansiStyle {
	switch {
	case p.Level >= ColorTrue:  return colorFgTrue(255, 255, 255)
	case p.Level >= Color256:   return colorFg256(255)
	case p.Level >= Color16:    return withANSI("\033[1;37m")
	default:                    return func(s string) string { return s }
	}
}

func apply(p ansiStyle, s string) string {
	if p == nil { return s }
	return p(s)
}

var ansiStripPattern = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func visualWidth(s string) int {
	stripped := ansiStripPattern.ReplaceAllString(s, "")
	w := 0
	for _, r := range stripped {
		if r < 32 { continue }
		w++
	}
	return w
}

func centerText(text string, width int) string {
	lines := strings.Split(text, "\n")
	out := make([]string, len(lines))
	for i, line := range lines {
		lineW := visualWidth(line)
		if lineW >= width {
			out[i] = line
		} else {
			pad := (width - lineW) / 2
			out[i] = strings.Repeat(" ", pad) + line
		}
	}
	return strings.Join(out, "\n")
}

func DisplayLogo(profile TerminalProfile) {
	logo := tui.RenderedLogoTerm()
	fmt.Print("\n")
	fmt.Println(centerText(logo, profile.Width))
	fmt.Print("\n")
}

func DisplayWelcome(profile TerminalProfile, username string) {
	fmt.Printf("Welcome, %s. Type /help for commands.\n\n", username)
}

func ShowPrompt(profile TerminalProfile) {
	fmt.Print(apply(newPalette(profile.ColorLevel).brightWhite(), "You> "))
}

func ShowPendingPrompt(profile TerminalProfile) {
	fmt.Print(apply(newPalette(profile.ColorLevel).cyan(), "...> "))
}

// streamState tracks the current streaming mode to insert correct spacing.
type streamState struct {
	hadReasoning bool
	hadAssistant bool
	atLineStart  bool
	col          int
	width        int
}

func newStreamState(width int) *streamState {
	return &streamState{atLineStart: true, width: width}
}

func (s *streamState) onReasoningDelta(profile TerminalProfile, text string) {
	if !s.hadReasoning && s.hadAssistant {
		fmt.Print("\n\n")
		s.atLineStart = true
	}
	s.hadReasoning = true
	if s.atLineStart {
		fmt.Print("  ")
		s.atLineStart = false
	}
	fmt.Print(apply(newPalette(profile.ColorLevel).muted(), text))
}

func (s *streamState) onAssistantDelta(profile TerminalProfile, text string) {
	if s.hadReasoning && !s.hadAssistant {
		fmt.Print("\n\n")
		s.atLineStart = true
		s.col = 0
	}
	s.hadAssistant = true
	if s.atLineStart && strings.HasPrefix(text, "\n") {
		text = strings.TrimLeft(text, "\n")
		s.atLineStart = true
		s.col = 0
	}

	pal := newPalette(profile.ColorLevel)
	bodyStart := pal.bodyCode()

	if bodyStart != "" {
		styled := inlineFormat(text, profile)
		styled = strings.ReplaceAll(styled, resetCode, resetCode+bodyStart)
		styled = bodyStart + styled + resetCode
		s.writeWrapped(styled, profile)
	} else {
		s.writeWrapped(text, profile)
	}
	s.atLineStart = false
}

func (s *streamState) writeWrapped(text string, profile TerminalProfile) {
	wrapWidth := s.width
	if wrapWidth < 40 {
		wrapWidth = 80
	}
	pal := newPalette(profile.ColorLevel)
	bodyCode := pal.bodyCode()

	emitNewline := func() {
		fmt.Print("\r\n")
		s.col = 0
		if bodyCode != "" {
			fmt.Print(bodyCode)
		}
	}

	for i := 0; i < len(text); {
		if text[i] == '\x1b' {
			end := strings.IndexByte(text[i:], 'm')
			if end < 0 {
				fmt.Print(string(text[i]))
				i++
				continue
			}
			fmt.Print(text[i : i+end+1])
			i += end + 1
			continue
		}

		r, size := utf8.DecodeRuneInString(text[i:])
		if r == '\n' {
			emitNewline()
			i += size
			continue
		}
		if r < 32 {
			i += size
			continue
		}

		if s.col >= wrapWidth && r == ' ' {
			emitNewline()
			i += size
			continue
		}
		if s.col >= wrapWidth {
			emitNewline()
		}

		fmt.Print(string(r))
		s.col++
		i += size
	}
}

func (s *streamState) onToolCall(profile TerminalProfile, name, args string) {
	if !s.atLineStart {
		fmt.Println()
	}
	fmt.Print("  ")
	fmt.Print(apply(dim, bold("⏺")))
	fmt.Print(" ")
	fmt.Print(apply(newPalette(profile.ColorLevel).yellow(), name))
	if args != "" {
		fmt.Print(apply(newPalette(profile.ColorLevel).muted(), "("+truncate(args, 60)+")"))
	}
	fmt.Println()
	s.atLineStart = true
}

func (s *streamState) onToolResult(profile TerminalProfile, result string) {
	pal := newPalette(profile.ColorLevel)
	preview := truncate(strings.TrimSpace(result), 120)
	for _, line := range strings.Split(preview, "\n") {
		if line == "" { continue }
		fmt.Print("    ")
		fmt.Print(apply(dim, apply(pal.muted(), "→")))
		fmt.Print(" ")
		fmt.Println(apply(pal.muted(), line))
	}
	s.atLineStart = true
}

func (s *streamState) onError(profile TerminalProfile, errMsg string) {
	if !s.atLineStart { fmt.Println() }
	fmt.Println(apply(newPalette(profile.ColorLevel).red(), "Error: "+errMsg))
	s.atLineStart = true
}

func PrintSystem(profile TerminalProfile, text string) {
	fmt.Println(apply(newPalette(profile.ColorLevel).muted(), text))
}

func PrintError(profile TerminalProfile, errMsg string) {
	fmt.Println(apply(newPalette(profile.ColorLevel).red(), "Error: "+errMsg))
}

func PrintExitSummary(profile TerminalProfile, convID int64, tools int, inputTokens, outputTokens int, cost float64) {
	pal := newPalette(profile.ColorLevel)

	fmt.Print("\r\033[K")
	fmt.Println()

	parts := []string{}
	parts = append(parts, apply(pal.amber(), bold("Goodbye.")))

	parts = append(parts, fmt.Sprintf("Conv %s", apply(pal.muted(), fmt.Sprintf("r%d", convID))))

	if tools > 0 {
		parts = append(parts, fmt.Sprintf("Tools %s", apply(pal.muted(), fmt.Sprintf("%d", tools))))
	}
	total := inputTokens + outputTokens
	if total > 0 {
		parts = append(parts, fmt.Sprintf("Tokens %s", apply(pal.muted(), fmt.Sprintf("%d↑ / %d↓", inputTokens, outputTokens))))
	}
	if cost > 0 {
		parts = append(parts, fmt.Sprintf("Cost %s", apply(pal.muted(), fmt.Sprintf("$%.4f", cost))))
	}

	fmt.Println("  " + strings.Join(parts, "  │  "))
	fmt.Println()
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen { return s }
	return s[:maxLen-1] + "…"
}

var (
	ifBoldStar   = regexp.MustCompile(`\*\*(.+?)\*\*`)
	ifBoldUnd    = regexp.MustCompile(`__(.+?)__`)
	ifItalicStar = regexp.MustCompile(`\*([^*\n]+)\*`)
	ifItalicUnd  = regexp.MustCompile(`_([^_\n]+)_`)
	ifCode       = regexp.MustCompile("`([^`\n]+)`")
)

func inlineFormat(text string, profile TerminalProfile) string {
	if profile.ColorLevel == ColorNone {
		return text
	}
	text = ifBoldStar.ReplaceAllStringFunc(text, func(m string) string {
		inner := ifBoldStar.FindStringSubmatch(m)[1]
		return bold(inner)
	})
	text = ifBoldUnd.ReplaceAllStringFunc(text, func(m string) string {
		inner := ifBoldUnd.FindStringSubmatch(m)[1]
		return bold(inner)
	})
	text = ifItalicStar.ReplaceAllStringFunc(text, func(m string) string {
		inner := ifItalicStar.FindStringSubmatch(m)[1]
		return italic(inner)
	})
	text = ifItalicUnd.ReplaceAllStringFunc(text, func(m string) string {
		inner := ifItalicUnd.FindStringSubmatch(m)[1]
		return italic(inner)
	})
	text = ifCode.ReplaceAllStringFunc(text, func(m string) string {
		inner := ifCode.FindStringSubmatch(m)[1]
		return dim(reverse(inner))
	})
	return text
}

// wrapWords breaks text into lines at word boundaries, respecting maxWidth.
func wrapWords(text string, maxWidth int) string {
	if maxWidth <= 0 { return text }
	var out strings.Builder
	remaining := text
	for len(remaining) > 0 {
		chunk := remaining
		if visualWidth(chunk) <= maxWidth {
			out.WriteString(chunk)
			break
		}
		cut := 0
		lastSpace := -1
		width := 0
		for i, r := range chunk {
			rw := runeWidth(r)
			if width+rw > maxWidth {
				if lastSpace >= 0 {
					cut = lastSpace
				} else {
					cut = i
				}
				break
			}
			width += rw
			if r == ' ' {
				lastSpace = i + utf8.RuneLen(r)
			}
		}
		if cut == 0 {
			cut = len(chunk)
		}
		out.WriteString(chunk[:cut])
		out.WriteByte('\n')
		remaining = strings.TrimLeft(chunk[cut:], " ")
	}
	return out.String()
}

func runeWidth(r rune) int {
	if r < 32 { return 0 }
	return 1
}
