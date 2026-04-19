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

	// ── Backwards-compat: mapped to new palette ─────────────────────────
	colorPanel    = colorBotMsgBg  // code/table backgrounds
	colorPanelAlt = colorHeaderBg  // composer input bg
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

	styleToolMsg = styleToolStrip // backwards-compat alias

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
