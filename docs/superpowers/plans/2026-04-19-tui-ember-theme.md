# Tether TUI — Ember Theme Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement the Ember design system across all Tether TUI screens — palette, full-width strip messages, block-brand header, quiet-ghost tool calls, streaming dot animation, and restyled auth screen.

**Architecture:** Pure visual layer — zero logic changes. All changes are in `styles.go`, `app.go`, `chat.go`, `auth.go`, and `richtext.go`. The new design replaces floating chat bubbles with full-width strips coded by left-border color, adds glyphic sender labels, and adopts an amber accent palette.

**Tech Stack:** Go, `charm.land/lipgloss/v2`, `charm.land/bubbletea/v2`

---

## File Map

| File | Changes |
|---|---|
| `internal/tui/styles.go` | Full palette + all style variables replaced |
| `internal/tui/chat.go` | `formatMessage` signature + body; strip layout; sender glyphs; tool ghost; streaming dots; autocomplete; composer |
| `internal/tui/app.go` | `renderHeader()` block-brand; `dispatchNextWaitlist`/confirm dispatch stream ticker |
| `internal/tui/auth.go` | `View()` amber logo + mode-tab box + field rows + amber button |
| `internal/tui/richtext.go` | All `richColor` / `richXxxStyle` functions updated |

No new files. No changes to `store/`, `agent/`, or any business logic.

---

## Task 1: Replace color palette and base styles

**Files:**
- Modify: `internal/tui/styles.go`

- [ ] **Step 1: Replace the entire contents of `styles.go`**

```go
package tui

import "charm.land/lipgloss/v2"

// Color palette — ANSI 256 for broad SSH terminal compatibility.
// Dark green user-bubble bg has no good ANSI 256 equivalent; truecolor is used.
var (
	colorAmber      = lipgloss.Color("172")      // #e5890a — amber accent
	colorGreen      = lipgloss.Color("71")        // #3fb950 — user / success
	colorRed        = lipgloss.Color("203")       // #f85149 — error
	colorBg         = lipgloss.Color("233")       // #111 — main background
	colorHeaderBg   = lipgloss.Color("232")       // #0e0e0e — header / composer bg
	colorBotMsgBg   = lipgloss.Color("234")       // #161616 — assistant strip
	colorUserMsgBg  = lipgloss.Color("#0f1a12")   // dark green tint — user strip
	colorToolBg     = lipgloss.Color("233")       // #131313 — tool strip
	colorBorder     = lipgloss.Color("235")       // #1e1e1e — borders
	colorToolBorder = lipgloss.Color("236")       // #252525 — tool strip border
	colorMuted      = lipgloss.Color("240")       // #555
	colorDim        = lipgloss.Color("236")       // #333
	colorBody       = lipgloss.Color("252")       // #ccc — main body text
	colorUserBody   = lipgloss.Color("115")       // #a3e4b2 — user body text
)

var (
	styleTitle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("255"))
	styleDim    = lipgloss.NewStyle().Foreground(colorDim)
	styleMuted  = lipgloss.NewStyle().Foreground(colorMuted)
	styleAccent = lipgloss.NewStyle().Foreground(colorAmber)

	// ── Header bar ────────────────────────────────────────────────────────
	styleHeaderBrand = lipgloss.NewStyle().
				Background(colorAmber).
				Foreground(lipgloss.Color("232")). // black on amber
				Bold(true).
				Padding(0, 2)

	styleTab = lipgloss.NewStyle().
			Background(colorHeaderBg).
			Foreground(colorDim).
			Padding(0, 2)

	styleTabActive = lipgloss.NewStyle().
			Background(lipgloss.Color("234")). // slightly lighter than header
			Foreground(lipgloss.Color("255")).
			Bold(true).
			Padding(0, 2).
			BorderTop(true).
			BorderStyle(lipgloss.NormalBorder()).
			BorderForeground(colorAmber)

	styleHeaderSpacer = lipgloss.NewStyle().Background(colorHeaderBg)

	styleHeaderUser = lipgloss.NewStyle().
			Background(colorHeaderBg).
			Foreground(colorDim).
			Padding(0, 1)

	// ── Auth screen ───────────────────────────────────────────────────────
	styleLogo = lipgloss.NewStyle().Foreground(colorAmber)

	styleAuthBox = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorBorder).
			Padding(0, 0)

	styleAuthModeHeader = lipgloss.NewStyle().
				Background(colorHeaderBg).
				Foreground(colorDim).
				Padding(0, 2)

	styleAuthModeHeaderActive = lipgloss.NewStyle().
					Background(colorHeaderBg).
					Foreground(colorAmber).
					Bold(true).
					Padding(0, 2)

	styleAuthFieldLabel = lipgloss.NewStyle().
				Foreground(colorDim).
				Width(10)

	styleAuthFieldValue = lipgloss.NewStyle().
				Foreground(colorMuted)

	styleAuthSubmit = lipgloss.NewStyle().
			Background(colorAmber).
			Foreground(lipgloss.Color("232")).
			Bold(true).
			Align(lipgloss.Center).
			Padding(0, 2)

	// ── Chat banner ───────────────────────────────────────────────────────
	styleChatBanner = lipgloss.NewStyle().
			Background(lipgloss.Color("232")).
			Foreground(colorDim).
			Padding(0, 1)

	styleChatBannerKey = lipgloss.NewStyle().
				Background(colorBotMsgBg).
				Foreground(colorMuted).
				Padding(0, 1)

	// ── Chat transcript ───────────────────────────────────────────────────
	styleChatTranscript = lipgloss.NewStyle()

	// Full-width strip styles — left border codes the sender.
	styleUserMsg = lipgloss.NewStyle().
			Background(colorUserMsgBg).
			Foreground(colorUserBody).
			BorderLeft(true).
			BorderStyle(lipgloss.ThickBorder()).
			BorderForeground(colorGreen).
			Padding(0, 1)

	styleAgentMsg = lipgloss.NewStyle().
			Background(colorBotMsgBg).
			Foreground(colorBody).
			BorderLeft(true).
			BorderStyle(lipgloss.ThickBorder()).
			BorderForeground(colorAmber).
			Padding(0, 1)

	styleSystemMsg = lipgloss.NewStyle().
			Background(colorBg).
			Foreground(colorDim).
			BorderLeft(true).
			BorderStyle(lipgloss.ThickBorder()).
			BorderForeground(colorToolBorder).
			Padding(0, 1).
			Italic(true)

	styleErrorMsg = lipgloss.NewStyle().
			Background(colorBg).
			Foreground(lipgloss.Color("167")). // muted red
			BorderLeft(true).
			BorderStyle(lipgloss.ThickBorder()).
			BorderForeground(colorRed).
			Padding(0, 1).
			Italic(true)

	styleInfoMsg = lipgloss.NewStyle().
			Background(colorBg).
			Foreground(lipgloss.Color("71")). // same as green
			BorderLeft(true).
			BorderStyle(lipgloss.ThickBorder()).
			BorderForeground(colorGreen).
			Padding(0, 1)

	// Tool call strips
	styleToolStrip = lipgloss.NewStyle().
			Background(colorToolBg).
			Foreground(colorDim).
			BorderLeft(true).
			BorderStyle(lipgloss.ThickBorder()).
			BorderForeground(colorToolBorder).
			Padding(0, 1)

	styleToolResult = lipgloss.NewStyle().
			Background(colorBg).
			Foreground(colorDim).
			BorderLeft(true).
			BorderStyle(lipgloss.ThickBorder()).
			BorderForeground(lipgloss.Color("234")). // barely-visible
			Padding(0, 0, 0, 3).                    // extra left padding (indent)
			Italic(true)

	// Sender labels
	styleSenderUser   = lipgloss.NewStyle().Bold(true).Foreground(colorGreen)
	styleSenderBot    = lipgloss.NewStyle().Bold(true).Foreground(colorAmber)
	styleSenderSystem = lipgloss.NewStyle().Foreground(colorDim)

	// Reasoning
	styleReasoningHeader = lipgloss.NewStyle().Foreground(colorMuted)
	styleReasoningHint   = lipgloss.NewStyle().Foreground(colorDim)

	// ── Composer ─────────────────────────────────────────────────────────
	styleChatComposer = lipgloss.NewStyle().
				Background(colorHeaderBg).
				BorderTop(true).
				BorderStyle(lipgloss.NormalBorder()).
				BorderForeground(colorBorder).
				Padding(0, 1)

	styleChatInputBox = lipgloss.NewStyle().
				Background(colorHeaderBg).
				Foreground(lipgloss.Color("255"))

	styleChatPrompt = lipgloss.NewStyle().
			Foreground(colorAmber).
			Bold(true)

	styleChatEnterKey = lipgloss.NewStyle().
				Foreground(colorAmber).
				Border(lipgloss.NormalBorder()).
				BorderForeground(colorToolBorder).
				Padding(0, 1)

	styleChatHint = lipgloss.NewStyle().Foreground(colorDim)

	styleChatHintKey = lipgloss.NewStyle().
				Background(colorBotMsgBg).
				Foreground(colorMuted).
				Padding(0, 0)

	// ── Autocomplete ─────────────────────────────────────────────────────
	styleAutocompleteSuggestion = lipgloss.NewStyle().
					Background(colorHeaderBg).
					Foreground(colorMuted).
					Padding(0, 1)

	styleAutocompleteSuggestionActive = lipgloss.NewStyle().
						Background(colorBotMsgBg).
						Foreground(colorAmber).
						Bold(true).
						BorderLeft(true).
						BorderStyle(lipgloss.ThickBorder()).
						BorderForeground(colorAmber).
						Padding(0, 1)

	// ── Status / feedback ─────────────────────────────────────────────────
	styleError = lipgloss.NewStyle().Foreground(colorRed)
	styleInfo  = lipgloss.NewStyle().Foreground(colorGreen)

	styleDivider = lipgloss.NewStyle().Foreground(colorBorder)

	// ── Legacy aliases ────────────────────────────────────────────────────
	styleButton       = styleTab
	styleButtonActive = styleTabActive
)
```

- [ ] **Step 2: Verify the package compiles**

```bash
cd /home/chromatischer/Projects/Tether && go build ./internal/tui/...
```

Expected: no errors. (Some downstream style usages will be broken until Task 2, which is fine at this point if the style variables exist.)

- [ ] **Step 3: Commit**

```bash
git add internal/tui/styles.go
git commit -m "style: replace palette with Ember theme (amber + green + strip layout)"
```

---

## Task 2: Update message formatting — all four roles

**Files:**
- Modify: `internal/tui/chat.go`
- Modify: `internal/tui/chat_test.go`

The `formatMessage` function gains a `frame int` parameter (used in Task 4 for streaming dots; pass `0` until then). All four message roles move from floating bubbles to full-width strips.

- [ ] **Step 1: Update the failing tests first**

The tests that call `formatMessage` directly need `frame` as a third param, and the expected strings change. Replace the four affected test functions in `chat_test.go`:

```go
func TestFormatMessage_AssistantWithoutSeparateReasoningShowsExplicitNotice(t *testing.T) {
	out := formatMessage(chatMessage{role: "assistant", content: "Hi"}, 80, 0)
	if !strings.Contains(out, "no separate model reasoning") {
		t.Fatalf("expected no-reasoning notice, got %q", out)
	}
}

func TestFormatMessage_AssistantWithReasoningShowsModelReasoningLabel(t *testing.T) {
	out := formatMessage(chatMessage{role: "assistant", content: "Hi", reasoning: "step 1"}, 80, 0)
	if !strings.Contains(out, "model reasoning available") {
		t.Fatalf("expected reasoning affordance, got %q", out)
	}
}

func TestFormatMessage_SystemNoticeDoesNotRenderNoticeLabel(t *testing.T) {
	out := formatMessage(chatMessage{role: "system", content: "Started a fresh conversation."}, 80, 0)
	if !strings.Contains(out, "Started a fresh conversation.") {
		t.Fatalf("expected notice content, got %q", out)
	}
	if strings.Contains(out, "notice") {
		t.Fatalf("expected no notice label, got %q", out)
	}
}

func TestFormatMessage_ToolCallShowsResultInSameWidget(t *testing.T) {
	out := formatMessage(chatMessage{role: "tool_call", content: "bash  {\"command\":\"pwd\"}\n\n{\n  \"exit_code\": 0,\n  \"stdout\": \"/work\\n\"\n}"}, 80, 0)
	if !strings.Contains(out, "▷") {
		t.Fatalf("expected tool icon ▷, got %q", out)
	}
	if !strings.Contains(out, "bash") {
		t.Fatalf("expected tool name bash, got %q", out)
	}
	if !strings.Contains(out, "\"stdout\": \"/work") {
		t.Fatalf("expected result content, got %q", out)
	}
}

func TestFormatMessage_StreamingReasoningRendersAtTopOfBubble(t *testing.T) {
	out := formatMessage(chatMessage{role: "assistant", content: "...", reasoning: "step 1", streaming: true}, 80, 0)
	if !strings.Contains(out, "◈ model reasoning") {
		t.Fatalf("expected streaming reasoning label, got %q", out)
	}
	if !strings.Contains(out, "step 1") {
		t.Fatalf("expected streaming reasoning text, got %q", out)
	}
}

func TestFormatMessage_StreamingReasoningHidesPlaceholderBody(t *testing.T) {
	out := formatMessage(chatMessage{role: "assistant", content: "...", reasoning: "step 1", streaming: true}, 80, 0)
	if strings.Contains(out, "\n\n...") {
		t.Fatalf("expected placeholder body to be hidden while only reasoning is streaming, got %q", out)
	}
}
```

Also add two new tests at the end of `chat_test.go`:

```go
func TestFormatMessage_UserMessageHasSenderGlyph(t *testing.T) {
	out := formatMessage(chatMessage{role: "user", content: "hello"}, 80, 0)
	if !strings.Contains(out, "you ›") {
		t.Fatalf("expected sender glyph 'you ›', got %q", out)
	}
	if !strings.Contains(out, "hello") {
		t.Fatalf("expected message content, got %q", out)
	}
}

func TestFormatMessage_SystemErrorPrefixGetsErrorStyle(t *testing.T) {
	out := formatMessage(chatMessage{role: "system", content: "failed to load messages: db error"}, 80, 0)
	if !strings.Contains(out, "✗ error") {
		t.Fatalf("expected error sender label, got %q", out)
	}
}

func TestFormatMessage_SystemSuccessPrefixGetsInfoStyle(t *testing.T) {
	out := formatMessage(chatMessage{role: "system", content: "memory added (id 7)"}, 80, 0)
	if !strings.Contains(out, "✓ info") {
		t.Fatalf("expected info sender label, got %q", out)
	}
}

func TestFormatMessage_ToolCallInProgressShowsRunning(t *testing.T) {
	out := formatMessage(chatMessage{role: "tool_call", content: "bash  pwd"}, 80, 0)
	if !strings.Contains(out, "▷") {
		t.Fatalf("expected tool icon, got %q", out)
	}
	if !strings.Contains(out, "running") {
		t.Fatalf("expected in-progress indicator, got %q", out)
	}
}
```

- [ ] **Step 2: Run tests to confirm they fail**

```bash
cd /home/chromatischer/Projects/Tether && go test ./internal/tui/... -run TestFormatMessage -v 2>&1 | head -40
```

Expected: compile error — `formatMessage` still has the old two-parameter signature and old logic.

- [ ] **Step 3: Replace `formatMessage` and add `systemMessageVariant` in `chat.go`**

Find the `formatMessage` function (around line 531) and replace it entirely, plus add `systemMessageVariant` just after it:

```go
// formatMessage renders a single chat message as a full-width left-border strip.
// frame drives the streaming dot animation; pass 0 when not animating.
func formatMessage(msg chatMessage, width int, frame int) string {
	if width <= 0 {
		width = 80
	}

	switch msg.role {
	case "user":
		label := styleSenderUser.Render("you ›")
		bodyW := max(16, width-styleUserMsg.GetHorizontalFrameSize())
		body := lipgloss.Wrap(msg.content, bodyW, " ")
		return styleUserMsg.Width(width).Render(label + "\n" + body)

	case "assistant":
		label := styleSenderBot.Render("◆ tether")
		bodyW := max(16, width-styleAgentMsg.GetHorizontalFrameSize())
		var parts []string

		// Reasoning block
		if strings.TrimSpace(msg.reasoning) != "" {
			header := "◈ model reasoning"
			if msg.streaming && (strings.TrimSpace(msg.content) == "" || msg.content == "...") {
				parts = append(parts,
					styleReasoningHeader.Render(header),
					lipgloss.Wrap(msg.reasoning, bodyW, " "),
				)
			} else if msg.reasoningExpanded {
				parts = append(parts,
					styleReasoningHeader.Render(header+"  ")+styleReasoningHint.Render("^O to collapse"),
					lipgloss.Wrap(msg.reasoning, bodyW, " "),
				)
			} else {
				parts = append(parts,
					styleDim.Render("◈ model reasoning available  ")+styleReasoningHint.Render("^O to expand"),
				)
			}
		} else if !msg.streaming {
			parts = append(parts, styleDim.Render("◈ no separate model reasoning"))
		}

		// Body / streaming dots
		body := strings.TrimSpace(msg.content)
		if msg.streaming && (body == "" || body == "...") {
			// Animated dot pulse: four brightness levels, each dot offset by 1 frame.
			dotLevels := []lipgloss.Style{
				lipgloss.NewStyle().Foreground(colorDim),
				lipgloss.NewStyle().Foreground(colorMuted),
				lipgloss.NewStyle().Foreground(colorAmber),
				lipgloss.NewStyle().Foreground(colorMuted),
			}
			d := func(offset int) string { return dotLevels[(frame+offset)%4].Render("●") }
			parts = append(parts, d(0)+" "+d(1)+" "+d(2))
		} else if body != "" {
			parts = append(parts, renderRichText(body, bodyW, richTextAssistant))
		}

		return styleAgentMsg.Width(width).Render(label + "\n" + strings.Join(parts, "\n"))

	case "tool_call":
		if !isValidToolCallContent(msg.content) {
			senderLabel, s := systemMessageVariant(msg.content)
			bodyW := max(16, width-s.GetHorizontalFrameSize())
			rendered := renderRichText(msg.content, bodyW, richTextSystem)
			return s.Width(width).Render(styleSenderSystem.Render(senderLabel) + "\n" + rendered)
		}
		entry, _ := parseToolCallContent(msg.content)
		invLine := "▷  " + entry.Name
		if args := strings.TrimSpace(entry.Args); args != "" {
			invLine += "  ·  " + args
		}
		invRow := styleToolStrip.Width(width).Render(invLine)
		if result := strings.TrimSpace(entry.Result); result != "" {
			return invRow + "\n" + styleToolResult.Width(width).Render("✓  "+result)
		}
		return invRow + "\n" + styleToolResult.Width(width).Render("·  running…")

	default: // system
		senderLabel, s := systemMessageVariant(msg.content)
		bodyW := max(16, width-s.GetHorizontalFrameSize())
		rendered := renderRichText(msg.content, bodyW, richTextSystem)
		return s.Width(width).Render(styleSenderSystem.Render(senderLabel) + "\n" + rendered)
	}
}

// systemMessageVariant picks a sender label and style based on the content prefix.
func systemMessageVariant(content string) (string, lipgloss.Style) {
	c := strings.ToLower(strings.TrimSpace(content))
	errorPrefixes := []string{
		"(agent error)", "failed", "invalid", "unknown command",
		"secrets unavailable", "error:", "✗",
	}
	for _, p := range errorPrefixes {
		if strings.HasPrefix(c, p) {
			return "✗ error", styleErrorMsg
		}
	}
	successPrefixes := []string{
		"signal linked", "discord linked", "discord unlinked", "signal unlinked",
		"memory added", "memory updated", "memory deleted",
		"task added", "task updated", "task marked done",
		"secret stored", "deleted secret", "cleared all secrets",
		"updated role", "started a fresh", "spawned subagent",
	}
	for _, p := range successPrefixes {
		if strings.HasPrefix(c, p) {
			return "✓ info", styleInfoMsg
		}
	}
	return "● system", styleSystemMsg
}
```

- [ ] **Step 4: Update `reflow()` to pass `m.streamFrame`**

Find the `reflow()` method (around line 371) and change the `formatMessage` call:

```go
func (m *chatModel) reflow() {
	if m.viewport.Width() <= 0 {
		return
	}
	w := m.viewport.Width()
	lines := make([]string, 0, len(m.messages))
	for _, msg := range m.messages {
		lines = append(lines, formatMessage(msg, w, m.streamFrame))
	}
	m.viewport.SetContent(strings.Join(lines, "\n\n"))
}
```

Add `streamFrame int` to the `chatModel` struct (around line 47):

```go
type chatModel struct {
	db     *sql.DB
	userID int64
	convID int64

	width  int
	height int

	viewport              viewport.Model
	textarea              textarea.Model
	messages              []chatMessage
	streamFrame           int // drives streaming dot animation
	streamingAssistantIdx map[int]int
	streamingToolCalls    map[int]map[string]int
	dataDir               string
	isAdmin               bool
	skills                []string
	focus                 composerFocus
	suggestions           []chatSuggestion
	selectedSuggestion    int
	autocompleteDismissed bool

	polling bool
	err     error
}
```

- [ ] **Step 5: Run tests**

```bash
cd /home/chromatischer/Projects/Tether && go test ./internal/tui/... -run TestFormatMessage -v
```

Expected: all `TestFormatMessage_*` tests pass.

- [ ] **Step 6: Run full test suite**

```bash
cd /home/chromatischer/Projects/Tether && go test ./internal/tui/... -v 2>&1 | tail -20
```

Expected: all tests pass.

- [ ] **Step 7: Commit**

```bash
git add internal/tui/chat.go internal/tui/chat_test.go
git commit -m "style: full-width strip messages with sender glyphs and quiet-ghost tool calls"
```

---

## Task 3: Update composer View() and autocomplete rendering

**Files:**
- Modify: `internal/tui/chat.go`

Replace the banner and composer rendering so they use the new Ember styles.

- [ ] **Step 1: Replace the `View()` method on `chatModel`**

Find `func (m chatModel) View() tea.View` (around line 343) and replace its body:

```go
func (m chatModel) View() tea.View {
	innerW := max(20, m.width)

	// Banner: dim keyboard hints
	bannerHints := []string{
		styleChatBannerKey.Render("tab") + styleChatHint.Render(" autocomplete"),
		styleChatBannerKey.Render("/") + styleChatHint.Render(" commands"),
		styleChatBannerKey.Render("$") + styleChatHint.Render(" skills"),
		styleChatBannerKey.Render("^O") + styleChatHint.Render(" reasoning"),
	}
	banner := styleChatBanner.Width(innerW).Render(strings.Join(bannerHints, "  "))

	transcript := styleChatTranscript.Width(innerW).Render(m.viewport.View())

	composerW := max(18, innerW-styleChatComposer.GetHorizontalFrameSize())
	inputW := max(18, composerW-styleChatInputBox.GetHorizontalFrameSize())
	m.textarea.SetWidth(inputW - 3) // room for prompt and enter-key

	prompt := styleChatPrompt.Render("❯")
	inputBox := styleChatInputBox.Render(m.textarea.View())
	enterKey := styleChatEnterKey.Render("[enter]")
	inputRow := prompt + " " + inputBox + " " + enterKey

	hintsRow := strings.Join([]string{
		styleChatHintKey.Render("tab") + " cycle",
		styleChatHintKey.Render("↵") + " apply",
		styleChatHintKey.Render("esc") + " dismiss",
		styleChatHintKey.Render("^O") + " reasoning",
	}, "  ")
	hintsLine := styleChatHint.Render(hintsRow)

	composerBody := inputRow + "\n" + hintsLine
	if rendered := m.renderSuggestions(composerW); rendered != "" {
		composerBody = rendered + "\n" + composerBody
	}
	composer := styleChatComposer.Width(innerW).Render(composerBody)

	content := lipgloss.JoinVertical(lipgloss.Left, banner, transcript, composer)
	return tea.NewView(content)
}
```

- [ ] **Step 2: Replace `renderSuggestions` to use new styles**

Find `func (m chatModel) renderSuggestions(width int) string` (around line 774) and replace:

```go
func (m chatModel) renderSuggestions(width int) string {
	if len(m.suggestions) == 0 {
		return ""
	}
	rows := make([]string, 0, len(m.suggestions))
	for i, s := range m.suggestions {
		line := s.Label
		if strings.TrimSpace(s.Detail) != "" {
			line += "  " + styleDim.Render(s.Detail)
		}
		if m.focus == composerFocusSuggestion && i == m.selectedSuggestion {
			rows = append(rows, styleAutocompleteSuggestionActive.Width(width).Render(line))
		} else {
			rows = append(rows, styleAutocompleteSuggestion.Width(width).Render(line))
		}
	}
	return lipgloss.JoinVertical(lipgloss.Left, rows...)
}
```

- [ ] **Step 3: Update `composerHeight` to match the new composer body structure**

Find `func (m chatModel) composerHeight(innerW int) int` (around line 809) and replace:

```go
func (m chatModel) composerHeight(innerW int) int {
	composerW := max(18, innerW-styleChatComposer.GetHorizontalFrameSize())
	inputW := max(18, composerW-styleChatInputBox.GetHorizontalFrameSize())
	m.textarea.SetWidth(inputW - 3)
	inputRow := styleChatPrompt.Render("❯") + " " + styleChatInputBox.Render(m.textarea.View()) + " " + styleChatEnterKey.Render("[enter]")
	hintsLine := styleChatHint.Render("tab cycle  ↵ apply  esc dismiss  ^O reasoning")
	body := inputRow + "\n" + hintsLine
	if rendered := m.renderSuggestions(composerW); rendered != "" {
		body = rendered + "\n" + body
	}
	return lipgloss.Height(styleChatComposer.Width(innerW).Render(body))
}
```

- [ ] **Step 4: Update `withSize` textarea width to account for prompt and enter-key**

In `withSize` (around line 158), find `m.textarea.SetWidth(inputW)` and replace:

```go
promptAndEnterW := lipgloss.Width(styleChatPrompt.Render("❯")+" ") +
    lipgloss.Width(" "+styleChatEnterKey.Render("[enter]"))
m.textarea.SetWidth(max(10, inputW-promptAndEnterW))
```

Also remove the `m.textarea.SetWidth(inputW - 3)` line from the `View()` and `composerHeight()` methods added in Steps 1 and 3 above (since `withSize` now sets the correct width upfront).

- [ ] **Step 5: Build and run tests**

```bash
cd /home/chromatischer/Projects/Tether && go test ./internal/tui/... -v 2>&1 | tail -20
```

Expected: all tests pass.

- [ ] **Step 6: Commit**

```bash
git add internal/tui/chat.go
git commit -m "style: Ember composer with prompt glyph, kbd hints bar, and autocomplete left-border"
```

---

## Task 4: Streaming dot animation

**Files:**
- Modify: `internal/tui/chat.go`

Add a 120ms ticker that increments `streamFrame` so the three dots animate while an assistant response is loading.

- [ ] **Step 1: Add `streamTickMsg` type and helper methods near the top of the message types in `chat.go`**

After the `chatPollNotificationsMsg` type (around line 101) add:

```go
type streamTickMsg struct{}
```

After the `newChatModel` function (around line 138) add these two methods:

```go
func (m chatModel) hasStreamingMessages() bool {
	for _, msg := range m.messages {
		if msg.streaming {
			return true
		}
	}
	return false
}

func (m chatModel) streamTickCmd() tea.Cmd {
	return tea.Tick(120*time.Millisecond, func(time.Time) tea.Msg {
		return streamTickMsg{}
	})
}
```

- [ ] **Step 2: Handle `streamTickMsg` in `chatModel.Update()`**

In the `Update` method's `switch msg := msg.(type)` block, add a case after `chatNotificationsDeliveredMsg`:

```go
case streamTickMsg:
	if m.hasStreamingMessages() {
		m.streamFrame = (m.streamFrame + 1) % 8
		m.reflow()
		return m, m.streamTickCmd()
	}
	return m, nil
```

- [ ] **Step 3: Fire the ticker when streaming starts in `app.go`**

In `app.go`, find `func (m *appModel) dispatchNextWaitlist() tea.Cmd` and update it to batch the stream tick:

```go
func (m *appModel) dispatchNextWaitlist() tea.Cmd {
	if len(m.waitlist) == 0 {
		return nil
	}
	text := m.waitlist[0]
	m.waitlist = m.waitlist[1:]
	requestID := m.nextRequestID
	m.nextRequestID++
	m.activeRuns++
	m.releasedRuns[requestID] = false
	m.chat = m.chat.startStreamingAssistant(requestID)
	return tea.Batch(m.askAgentCmdWithID(requestID, text), m.chat.streamTickCmd())
}
```

Also find the `/confirm` branch in `handleCommand` (around line 575) and update the return:

```go
m.chat = m.chat.startStreamingAssistant(requestID)
return m, true, tea.Batch(m.resumeConfirmationCmd(requestID, fields[1]), m.chat.streamTickCmd())
```

- [ ] **Step 4: Verify `time` is imported in `chat.go`**

```bash
head -20 /home/chromatischer/Projects/Tether/internal/tui/chat.go
```

`time` should already be imported (it's used for `pollTickCmd`). If not, add `"time"` to the import block.

- [ ] **Step 5: Add a test for the streaming dot tick**

At the end of `chat_test.go` add:

```go
func TestStreamTickAdvancesFrameAndReflows(t *testing.T) {
	m := newChatModel()
	m = m.startStreamingAssistant(1)

	if !m.hasStreamingMessages() {
		t.Fatal("expected streaming message")
	}

	frame0 := m.streamFrame
	updated, cmd := m.Update(streamTickMsg{})
	if updated.streamFrame != frame0+1 {
		t.Fatalf("expected streamFrame to advance, got %d → %d", frame0, updated.streamFrame)
	}
	if cmd == nil {
		t.Fatal("expected ticker to re-fire while streaming")
	}
}

func TestStreamTickStopsAfterStreamingEnds(t *testing.T) {
	m := newChatModel()
	m = m.startStreamingAssistant(1)
	m = m.finishStreamingAssistant(1, "done", "")

	updated, cmd := m.Update(streamTickMsg{})
	_ = updated
	if cmd != nil {
		t.Fatal("expected ticker to stop after streaming finished")
	}
}
```

- [ ] **Step 6: Run tests**

```bash
cd /home/chromatischer/Projects/Tether && go test ./internal/tui/... -run TestStream -v
```

Expected: all stream-related tests pass.

- [ ] **Step 7: Full test suite**

```bash
cd /home/chromatischer/Projects/Tether && go test ./internal/tui/... 2>&1 | tail -10
```

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/tui/chat.go internal/tui/chat_test.go internal/tui/app.go
git commit -m "style: animated streaming dot indicator (120ms ticker, 4-level amber pulse)"
```

---

## Task 5: Update header bar — Block Brand

**Files:**
- Modify: `internal/tui/app.go`

Replace the single-line header with the Block Brand design: amber-filled brand block, tab strip with top-border active indicator, right-aligned user badge.

- [ ] **Step 1: Replace `renderHeader()` in `app.go`**

Find `func (m appModel) renderHeader() string` (around line 399) and replace the body:

```go
func (m appModel) renderHeader() string {
	if m.w <= 0 {
		return ""
	}

	brand := styleHeaderBrand.Render("TETHER")

	buttons := m.headerButtons()
	tabParts := make([]string, 0, len(buttons))
	for _, b := range buttons {
		active := false
		switch b.ID {
		case "login":
			active = m.view == viewLogin
		case "signup":
			active = m.view == viewSignup
		case "chat":
			active = m.view == viewChat
		case "memory":
			active = m.view == viewMemory
		case "settings":
			active = m.view == viewSettings
		case "admin":
			active = m.view == viewAdmin
		}
		if active {
			tabParts = append(tabParts, styleTabActive.Render("▸ "+b.Label))
		} else {
			tabParts = append(tabParts, styleTab.Render(b.Label))
		}
	}
	tabs := lipgloss.JoinHorizontal(lipgloss.Top, tabParts...)

	// Right side: online dot + username (only when logged in).
	var userBadge string
	if m.user != nil {
		dot := lipgloss.NewStyle().Foreground(lipgloss.Color("71")).Render("●")
		userBadge = styleHeaderUser.Render(dot + " " + m.user.Username)
	}

	usedW := lipgloss.Width(brand) + lipgloss.Width(tabs) + lipgloss.Width(userBadge)
	gap := m.w - usedW
	if gap < 0 {
		gap = 0
	}
	spacer := styleHeaderSpacer.Render(strings.Repeat(" ", gap))

	return brand + spacer + tabs + userBadge
}
```

- [ ] **Step 2: Build to check for compile errors**

```bash
cd /home/chromatischer/Projects/Tether && go build ./internal/tui/...
```

Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add internal/tui/app.go
git commit -m "style: Block Brand header — amber fill, top-border active tab, user badge"
```

---

## Task 6: Restyle the auth screen

**Files:**
- Modify: `internal/tui/auth.go`

Replace the `View()` with the Ember design: amber logo, mode-tab box header, field rows, amber submit button.

- [ ] **Step 1: Replace `View()` in `auth.go`**

Find `func (m authModel) View() tea.View` (line 144) and replace the entire function body:

```go
func (m authModel) View() tea.View {
	// ── Logo ─────────────────────────────────────────────────────────────
	logo := styleLogo.Render(tetherLogo)
	tagline := styleDim.Render("personal AI over SSH")

	// ── Mode header (tab switcher inside the box) ─────────────────────────
	var loginLabel, signupLabel string
	if m.mode == authModeLogin {
		loginLabel = styleAuthModeHeaderActive.Render("▸ Login")
		signupLabel = styleAuthModeHeader.Render("Sign up")
	} else {
		loginLabel = styleAuthModeHeader.Render("Login")
		signupLabel = styleAuthModeHeaderActive.Render("▸ Sign up")
	}
	modeRow := lipgloss.JoinHorizontal(lipgloss.Top, loginLabel, signupLabel)

	// ── Fields ────────────────────────────────────────────────────────────
	usernameRow := lipgloss.JoinHorizontal(lipgloss.Top,
		styleAuthFieldLabel.Render("username"),
		styleAuthFieldValue.Render(m.username.View()),
	)
	passwordRow := lipgloss.JoinHorizontal(lipgloss.Top,
		styleAuthFieldLabel.Render("password"),
		styleAuthFieldValue.Render(m.password.View()),
	)
	// ── Submit button ─────────────────────────────────────────────────────
	var submitLabel string
	if m.mode == authModeLogin {
		submitLabel = "LOGIN  →"
	} else {
		submitLabel = "SIGN UP  →"
	}
	// Compute form width from the wider of the two field rows, then size
	// divider and submit button to match.
	formWidth := max(lipgloss.Width(usernameRow), lipgloss.Width(passwordRow))
	divider := styleDim.Render(strings.Repeat("─", formWidth))
	submitBtn := styleAuthSubmit.Width(formWidth).Render(submitLabel)

	formInner := lipgloss.JoinVertical(lipgloss.Left,
		modeRow,
		divider,
		usernameRow,
		passwordRow,
		divider,
		submitBtn,
	)

	// ── Status ────────────────────────────────────────────────────────────
	if m.statusText != "" {
		var statusLine string
		if m.statusErr {
			statusLine = styleError.Render("✗ " + m.statusText)
		} else {
			statusLine = styleInfo.Render("✓ " + m.statusText)
		}
		formInner = lipgloss.JoinVertical(lipgloss.Left, formInner, "", statusLine)
	}

	box := styleAuthBox.Render(formInner)

	// ── Hint ──────────────────────────────────────────────────────────────
	hint := styleDim.Render("tab · switch field   enter · submit   ctrl+c · quit")

	// ── Stack centered ────────────────────────────────────────────────────
	block := lipgloss.JoinVertical(lipgloss.Center,
		logo,
		tagline,
		"",
		box,
		"",
		hint,
	)

	if m.width > 0 && m.height > 0 {
		return tea.NewView(lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, block))
	}
	if m.width > 0 {
		return tea.NewView(lipgloss.PlaceHorizontal(m.width, lipgloss.Center, block))
	}
	return tea.NewView(block)
}
```

- [ ] **Step 2: Build**

```bash
cd /home/chromatischer/Projects/Tether && go build ./internal/tui/...
```

Expected: no errors.

- [ ] **Step 3: Run full test suite**

```bash
cd /home/chromatischer/Projects/Tether && go test ./internal/tui/...
```

Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/tui/auth.go
git commit -m "style: Ember auth screen — amber logo, mode-tab box, amber submit button"
```

---

## Task 7: Update rich-text style functions

**Files:**
- Modify: `internal/tui/richtext.go`

Update the color functions to use the new Ember palette.

- [ ] **Step 1: Replace all `richXxxStyle` and `richBodyColor` functions in `richtext.go`**

Find and replace each function from line 261 onwards:

```go
func richBodyColor(variant richTextVariant) lipgloss.Color {
	if variant == richTextSystem {
		return colorDim
	}
	return colorBody
}

func richColor(assistant, system string, variant richTextVariant) lipgloss.Color {
	if variant == richTextSystem {
		return lipgloss.Color(system)
	}
	return lipgloss.Color(assistant)
}

func richStrongStyle(variant richTextVariant) lipgloss.Style {
	// Bold = white in assistant context, green-ish in system
	return lipgloss.NewStyle().Bold(true).Foreground(richColor("255", "115", variant))
}

func richEmphStyle(variant richTextVariant) lipgloss.Style {
	return lipgloss.NewStyle().Italic(true).Foreground(richColor("252", "245", variant))
}

func richHeadingStyle(variant richTextVariant, level int) lipgloss.Style {
	switch level {
	case 1:
		return lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("255"))
	case 2:
		return lipgloss.NewStyle().Bold(true).Foreground(colorBody)
	default:
		return lipgloss.NewStyle().Bold(true).Foreground(colorMuted)
	}
}

func richRuleStyle(variant richTextVariant) lipgloss.Style {
	return lipgloss.NewStyle().Foreground(richColor("172", "236", variant)) // amber / dim
}

func richLinkStyle(variant richTextVariant) lipgloss.Style {
	return lipgloss.NewStyle().Underline(true).Foreground(richColor("172", "245", variant)) // amber
}

func richMutedStyle(variant richTextVariant) lipgloss.Style {
	return lipgloss.NewStyle().Foreground(colorDim)
}

func richCodeStyle(variant richTextVariant) lipgloss.Style {
	return lipgloss.NewStyle().
		Foreground(richColor("222", "252", variant)). // warm yellow / white
		Background(colorBotMsgBg).
		Padding(0, 1)
}

func richCodeBlockStyle(variant richTextVariant) lipgloss.Style {
	return lipgloss.NewStyle().
		Foreground(richBodyColor(variant)).
		Background(colorBotMsgBg).
		BorderLeft(true).
		BorderStyle(lipgloss.ThickBorder()).
		BorderForeground(colorToolBorder).
		Padding(0, 1)
}

func richTableHeaderStyle(variant richTextVariant) lipgloss.Style {
	return lipgloss.NewStyle().Bold(true).Foreground(richColor("172", "245", variant)) // amber
}

func richTableCellStyle(variant richTextVariant) lipgloss.Style {
	return lipgloss.NewStyle().Foreground(richBodyColor(variant))
}

func richTableBorderStyle(variant richTextVariant) lipgloss.Style {
	return lipgloss.NewStyle().Foreground(colorToolBorder)
}
```

- [ ] **Step 2: Run tests (richtext tests are in `chat_test.go`)**

```bash
cd /home/chromatischer/Projects/Tether && go test ./internal/tui/... -run TestRenderRichText -v
```

Expected: all rich text tests pass.

- [ ] **Step 3: Full test suite**

```bash
cd /home/chromatischer/Projects/Tether && go test ./internal/tui/...
```

Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/tui/richtext.go
git commit -m "style: update rich-text colors for Ember palette (amber links, warm code blocks)"
```

---

## Task 8: Final build verification

- [ ] **Step 1: Full build**

```bash
cd /home/chromatischer/Projects/Tether && go build ./...
```

Expected: no errors.

- [ ] **Step 2: Full test run**

```bash
cd /home/chromatischer/Projects/Tether && go test ./... 2>&1 | tail -20
```

Expected: all packages PASS.

- [ ] **Step 3: Verify `.superpowers/` is gitignored**

```bash
grep -r superpowers /home/chromatischer/Projects/Tether/.gitignore 2>/dev/null || echo "not ignored"
```

If not present, add it:

```bash
echo ".superpowers/" >> /home/chromatischer/Projects/Tether/.gitignore
git add .gitignore
git commit -m "chore: gitignore .superpowers brainstorm artifacts"
```
