package tui

import "charm.land/lipgloss/v2"

// Color palette — ANSI 256 for broad SSH terminal compatibility.
// Dark green user-bubble bg has no good ANSI 256 equivalent; truecolor is used.
var (
	colorAmber      = lipgloss.Color("172")     // hard yellow-orange accent
	colorGreen      = lipgloss.Color("114")     // brighter green for status/user accents
	colorRed        = lipgloss.Color("210")     // softer bright red for errors
	colorBg         = lipgloss.Color("233")     // main background
	colorHeaderBg   = lipgloss.Color("232")     // header / composer bg
	colorBotMsgBg   = lipgloss.Color("234")     // assistant strip
	colorUserMsgBg  = lipgloss.Color("#132016") // slightly lighter green tint for user strip
	colorToolBg     = lipgloss.Color("234")     // tool strip
	colorBorder     = lipgloss.Color("238")     // visible but restrained borders
	colorToolBorder = lipgloss.Color("240")     // raised panels / tool block bg
	colorMuted      = lipgloss.Color("246")     // softer secondary text
	colorDim        = lipgloss.Color("242")     // subdued text
	colorBody       = lipgloss.Color("255")     // main body text
	colorUserBody   = lipgloss.Color("194")     // high-contrast user body text

	// ── Backwards-compat: mapped to new palette ─────────────────────────
	colorPanel    = colorBotMsgBg // code/table backgrounds
	colorPanelAlt = colorHeaderBg // composer input bg
)

var (
	styleTitle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("255"))
	styleDim    = lipgloss.NewStyle().Foreground(colorDim)
	styleMuted  = lipgloss.NewStyle().Foreground(colorMuted)
	styleAccent = lipgloss.NewStyle().Foreground(colorAmber)
	styleBodyBg = lipgloss.NewStyle().Background(colorBg)

	// ── Header bar ────────────────────────────────────────────────────────
	styleHeaderBrand = lipgloss.NewStyle().
				Background(colorHeaderBg).
				Foreground(colorBody).
				Bold(true).
				Padding(0, 2)

	styleTab = lipgloss.NewStyle().
			Background(colorHeaderBg).
			Foreground(colorMuted).
			Padding(0, 2)

	styleTabActive = lipgloss.NewStyle().
			Background(lipgloss.Color("234")). // slightly lighter than header
			Foreground(colorAmber).
			Bold(true).
			Padding(0, 2)

	styleHeaderSpacer = lipgloss.NewStyle().Background(colorHeaderBg)
	styleHeaderBar    = lipgloss.NewStyle().Background(colorHeaderBg)

	styleHeaderUserDot = lipgloss.NewStyle().
				Background(colorHeaderBg).
				Foreground(colorGreen).
				Padding(0, 1, 0, 1)

	styleHeaderUserText = lipgloss.NewStyle().
				Background(colorHeaderBg).
				Foreground(colorMuted).
				Padding(0, 1, 0, 0)

	// ── Auth screen ───────────────────────────────────────────────────────
	styleLogo = lipgloss.NewStyle().
			Background(colorBg).
			Foreground(colorAmber)

	styleAuthTagline = lipgloss.NewStyle().
				Background(colorBg).
				Foreground(colorMuted)

	styleAuthHint = lipgloss.NewStyle().
			Background(colorBg).
			Foreground(colorMuted)

	styleAuthBox = lipgloss.NewStyle().
			Background(colorHeaderBg).
			Border(lipgloss.RoundedBorder()).
			BorderBackground(colorBg).
			BorderForeground(colorBorder).
			Padding(0, 0)

	styleAuthModeHeader = lipgloss.NewStyle().
				Background(colorHeaderBg).
				Foreground(colorMuted).
				Padding(0, 2)

	styleAuthModeHeaderActive = lipgloss.NewStyle().
					Background(lipgloss.Color("234")).
					Foreground(colorAmber).
					Bold(true).
					Padding(0, 2)

	styleAuthFieldLabel = lipgloss.NewStyle().
				Background(colorHeaderBg).
				Foreground(colorMuted).
				Width(10)

	styleAuthFieldValue = lipgloss.NewStyle().
				Background(colorHeaderBg).
				Foreground(colorBody)

	styleAuthRow = lipgloss.NewStyle().
			Background(colorHeaderBg)

	styleAuthDivider = lipgloss.NewStyle().
				Background(colorHeaderBg).
				Foreground(colorMuted)

	styleAuthSubmit = lipgloss.NewStyle().
			Background(colorAmber).
			Foreground(lipgloss.Color("232")).
			Bold(true).
			Align(lipgloss.Center).
			Padding(0, 2)

	styleAuthStatusErr = lipgloss.NewStyle().
				Background(colorHeaderBg).
				Foreground(colorRed)

	styleAuthStatusOK = lipgloss.NewStyle().
				Background(colorHeaderBg).
				Foreground(colorGreen)

	// ── Chat banner ───────────────────────────────────────────────────────
	styleChatBanner = lipgloss.NewStyle().
			Background(lipgloss.Color("232")).
			Foreground(colorMuted).
			Padding(0, 1)

	styleChatBannerKey = lipgloss.NewStyle().
				Background(colorBotMsgBg).
				Foreground(colorBody).
				Padding(0, 1)

	// ── Chat transcript ───────────────────────────────────────────────────
	styleChatTranscript = lipgloss.NewStyle().Background(colorBg)

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
			Foreground(colorMuted).
			BorderLeft(true).
			BorderStyle(lipgloss.ThickBorder()).
			BorderForeground(colorToolBorder).
			Padding(0, 1).
			Italic(true)

	styleErrorMsg = lipgloss.NewStyle().
			Background(colorBg).
			Foreground(lipgloss.Color("217")).
			BorderLeft(true).
			BorderStyle(lipgloss.ThickBorder()).
			BorderForeground(colorRed).
			Padding(0, 1).
			Italic(true)

	styleInfoMsg = lipgloss.NewStyle().
			Background(colorBg).
			Foreground(colorGreen).
			BorderLeft(true).
			BorderStyle(lipgloss.ThickBorder()).
			BorderForeground(colorGreen).
			Padding(0, 1)

	// Tool call strips
	styleToolStrip = lipgloss.NewStyle().
			Background(colorToolBg).
			Foreground(colorMuted).
			BorderLeft(true).
			BorderStyle(lipgloss.ThickBorder()).
			BorderForeground(colorToolBorder).
			Padding(0, 1)

	styleToolResult = lipgloss.NewStyle().
			Background(colorBg).
			Foreground(colorMuted).
			BorderLeft(true).
			BorderStyle(lipgloss.ThickBorder()).
			BorderForeground(colorBorder).
			Padding(0, 0, 0, 3). // extra left padding (indent)
			Italic(true)

	// Sender labels
	styleSenderUser   = lipgloss.NewStyle().Bold(true).Foreground(colorGreen)
	styleSenderBot    = lipgloss.NewStyle().Bold(true).Foreground(colorAmber)
	styleSenderSystem = lipgloss.NewStyle().Foreground(colorMuted)

	// Reasoning
	styleReasoningHeader = lipgloss.NewStyle().Foreground(colorBody)
	styleReasoningHint   = lipgloss.NewStyle().Foreground(colorMuted)

	// ── Composer ─────────────────────────────────────────────────────────
	styleChatComposer = lipgloss.NewStyle().
				Background(colorHeaderBg).
				Padding(0, 1)

	styleChatInputBox = lipgloss.NewStyle().
				Background(colorHeaderBg).
				Foreground(lipgloss.Color("255")).
				Padding(0, 1)

	styleChatPromptGap = lipgloss.NewStyle().
				Background(colorHeaderBg)

	styleChatPrompt = lipgloss.NewStyle().
			Background(colorHeaderBg).
			Foreground(colorAmber).
			Bold(true)

	styleChatHint = lipgloss.NewStyle().
			Background(colorHeaderBg).
			Foreground(colorMuted)

	styleChatHintKey = lipgloss.NewStyle().
				Background(colorBotMsgBg).
				Foreground(colorBody).
				Padding(0, 1)

	styleChatHintText = lipgloss.NewStyle().
				Background(colorHeaderBg).
				Foreground(colorMuted)

	styleChatHintGap = lipgloss.NewStyle().
				Background(colorHeaderBg)

	styleChatRow = lipgloss.NewStyle().
			Background(colorHeaderBg)

	// ── Autocomplete ─────────────────────────────────────────────────────
	styleAutocompleteSuggestion = lipgloss.NewStyle().
					Background(colorHeaderBg).
					Foreground(colorBody).
					Padding(0, 1)

	styleAutocompleteSuggestionActive = lipgloss.NewStyle().
						Background(colorBotMsgBg).
						Foreground(colorAmber).
						Bold(true).
						BorderLeft(true).
						BorderStyle(lipgloss.ThickBorder()).
						BorderForeground(colorAmber).
						Padding(0, 1)

	// Detail text inside suggestion rows. Must carry an explicit background
	// matching the row it's rendered into — otherwise the detail span resets
	// mid-line and the row's bg breaks on those cells.
	styleAutocompleteDetail = lipgloss.NewStyle().
				Background(colorHeaderBg).
				Foreground(colorMuted)

	styleAutocompleteDetailActive = lipgloss.NewStyle().
					Background(colorBotMsgBg).
					Foreground(colorMuted)

	// ── Status / feedback ─────────────────────────────────────────────────
	styleError = lipgloss.NewStyle().Foreground(colorRed)
	styleInfo  = lipgloss.NewStyle().Foreground(colorGreen)

	styleTitleBg     = styleTitle.Background(colorBg)
	styleDimBg       = styleDim.Background(colorBg)
	styleMutedBg     = styleMuted.Background(colorBg)
	styleAccentBg    = styleAccent.Background(colorBg)
	styleSenderBotBg = styleSenderBot.Background(colorBg)
	styleErrorBg     = styleError.Background(colorBg)
	styleInfoBg      = styleInfo.Background(colorBg)

	styleDivider = lipgloss.NewStyle().Foreground(colorBorder)

	// ── Legacy aliases ────────────────────────────────────────────────────
	styleButton       = styleTab
	styleButtonActive = styleTabActive
)
