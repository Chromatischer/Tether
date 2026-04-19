package tui

import (
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// tetherLogo is the TETHER wordmark in figlet "shadow" block style.
// Each letter is rendered with Unicode box-drawing + full-block characters.
// Total width: 50 columns (fits any standard 80-col terminal).
const tetherLogo = `████████╗███████╗████████╗██╗  ██╗███████╗██████╗
╚══██╔══╝██╔════╝╚══██╔══╝██║  ██║██╔════╝██╔══██╗
   ██║   █████╗     ██║   ███████║█████╗  ██████╔╝
   ██║   ██╔══╝     ██║   ██╔══██║██╔══╝  ██╔══██╗
   ██║   ███████╗   ██║   ██║  ██║███████╗██║  ██║
   ╚═╝   ╚══════╝   ╚═╝   ╚═╝  ╚═╝╚══════╝╚═╝  ╚═╝`

type authMode int

const (
	authModeLogin authMode = iota
	authModeSignup
)

type authModel struct {
	mode authMode

	width  int
	height int

	username textinput.Model
	password textinput.Model

	focused int // 0=username, 1=password

	statusText string
	statusErr  bool
}

type authSubmitMsg struct {
	Mode     authMode
	Username string
	Password string
}

type authStatusMsg struct {
	Text  string
	IsErr bool
}

func newAuthModel(mode authMode) authModel {
	u := textinput.New()
	u.Placeholder = "username"
	u.Prompt = ""
	u.Focus()
	u.CharLimit = 64
	us := u.Styles()
	us.Focused.Text = us.Focused.Text.Background(colorHeaderBg).Foreground(lipgloss.Color("255"))
	us.Focused.Placeholder = us.Focused.Placeholder.Background(colorHeaderBg).Foreground(colorDim)
	us.Blurred.Text = us.Blurred.Text.Background(colorHeaderBg).Foreground(colorMuted)
	us.Blurred.Placeholder = us.Blurred.Placeholder.Background(colorHeaderBg).Foreground(colorDim)
	u.SetStyles(us)

	p := textinput.New()
	p.Placeholder = "password"
	p.Prompt = ""
	p.EchoMode = textinput.EchoPassword
	p.CharLimit = 256
	ps := p.Styles()
	ps.Focused.Text = ps.Focused.Text.Background(colorHeaderBg).Foreground(lipgloss.Color("255"))
	ps.Focused.Placeholder = ps.Focused.Placeholder.Background(colorHeaderBg).Foreground(colorDim)
	ps.Blurred.Text = ps.Blurred.Text.Background(colorHeaderBg).Foreground(colorMuted)
	ps.Blurred.Placeholder = ps.Blurred.Placeholder.Background(colorHeaderBg).Foreground(colorDim)
	p.SetStyles(ps)

	return authModel{
		mode:     mode,
		username: u,
		password: p,
		focused:  0,
	}
}

func (m authModel) withSize(w, h int) authModel {
	m.width = w
	m.height = h
	const inputWidth = 28
	m.username.SetWidth(inputWidth)
	m.password.SetWidth(inputWidth)
	return m
}

func (m authModel) Update(msg tea.Msg) (authModel, tea.Cmd) {
	switch msg := msg.(type) {
	case authStatusMsg:
		m.statusText = msg.Text
		m.statusErr = msg.IsErr
		return m, nil

	case tea.KeyPressMsg:
		switch msg.String() {
		case "tab", "shift+tab":
			m.focused = 1 - m.focused
			if m.focused == 0 {
				m.username.Focus()
				m.password.Blur()
			} else {
				m.password.Focus()
				m.username.Blur()
			}
			return m, nil

		case "enter":
			return m, func() tea.Msg {
				return authSubmitMsg{
					Mode:     m.mode,
					Username: strings.TrimSpace(m.username.Value()),
					Password: m.password.Value(),
				}
			}
		}

	case tea.MouseClickMsg:
		// Header is row 0; auth body starts at row 1.
		y := msg.Y - 1
		switch y {
		case 2:
			m.focused = 0
			m.username.Focus()
			m.password.Blur()
			return m, nil
		case 4:
			m.focused = 1
			m.password.Focus()
			m.username.Blur()
			return m, nil
		case 7:
			return m, func() tea.Msg {
				return authSubmitMsg{Mode: m.mode, Username: strings.TrimSpace(m.username.Value()), Password: m.password.Value()}
			}
		}
	}

	var cmd tea.Cmd
	if m.focused == 0 {
		m.username, cmd = m.username.Update(msg)
	} else {
		m.password, cmd = m.password.Update(msg)
	}
	return m, cmd
}

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
	modeRow = styleAuthRow.Width(formWidth).Render(modeRow)
	usernameRow = styleAuthRow.Width(formWidth).Render(usernameRow)
	passwordRow = styleAuthRow.Width(formWidth).Render(passwordRow)
	divider := styleAuthRow.Width(formWidth).Render(styleDim.Render(strings.Repeat("─", formWidth)))
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

	bgFill := lipgloss.NewStyle().Background(colorBg)
	if m.width > 0 && m.height > 0 {
		placed := lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, block)
		return tea.NewView(bgFill.Render(placed))
	}
	if m.width > 0 {
		placed := lipgloss.PlaceHorizontal(m.width, lipgloss.Center, block)
		return tea.NewView(bgFill.Render(placed))
	}
	return tea.NewView(block)
}

func (m authModel) cursor() *tea.Cursor {
	if m.focused == 0 {
		return m.username.Cursor()
	}
	return m.password.Cursor()
}
