package tui

import "charm.land/lipgloss/v2"

// Color palette — ANSI 256 for broad SSH terminal compatibility.
var (
	colorAccent = lipgloss.Color("63")  // purple-blue
	colorBg     = lipgloss.Color("235") // near-black (header bar)
	colorMuted  = lipgloss.Color("245") // medium gray
	colorDim    = lipgloss.Color("240") // dark gray
	colorBorder = lipgloss.Color("238") // subtle border
)

var (
	styleTitle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("255"))
	styleDim    = lipgloss.NewStyle().Foreground(colorDim)
	styleMuted  = lipgloss.NewStyle().Foreground(colorMuted)
	styleAccent = lipgloss.NewStyle().Foreground(colorAccent)

	// ── Header bar ────────────────────────────────────────────────────────
	styleHeaderBrand = lipgloss.NewStyle().
				Background(colorBg).
				Foreground(lipgloss.Color("255")).
				Bold(true).
				Padding(0, 2)

	styleTab = lipgloss.NewStyle().
			Background(colorBg).
			Foreground(colorMuted).
			Padding(0, 2)

	styleTabActive = lipgloss.NewStyle().
			Background(colorAccent).
			Foreground(lipgloss.Color("255")).
			Bold(true).
			Padding(0, 2)

	styleHeaderSpacer = lipgloss.NewStyle().Background(colorBg)

	// ── Auth screen ───────────────────────────────────────────────────────
	styleLogo = lipgloss.NewStyle().Foreground(colorAccent)

	styleAuthBox = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorBorder).
			Padding(1, 3)

	// ── Chat ──────────────────────────────────────────────────────────────
	styleSenderUser   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("255"))
	styleSenderBot    = lipgloss.NewStyle().Bold(true).Foreground(colorAccent)
	styleSenderSystem = lipgloss.NewStyle().Foreground(colorMuted)

	// Message row backgrounds (Width set dynamically in formatMessage).
	styleUserMsg = lipgloss.NewStyle().
			Background(lipgloss.Color("237")).
			Foreground(lipgloss.Color("253"))

	styleAgentMsg = lipgloss.NewStyle().
			Background(lipgloss.Color("233")).
			Foreground(lipgloss.Color("250"))

	styleToolMsg = lipgloss.NewStyle().
			Foreground(colorDim).
			Italic(true)

	styleDivider = lipgloss.NewStyle().Foreground(colorBorder)

	// ── Status / feedback ─────────────────────────────────────────────────
	styleError = lipgloss.NewStyle().Foreground(lipgloss.Color("161"))
	styleInfo  = lipgloss.NewStyle().Foreground(lipgloss.Color("74"))

	// ── Legacy aliases kept so other files compile without changes ─────────
	styleButton       = styleTab
	styleButtonActive = styleTabActive
)
