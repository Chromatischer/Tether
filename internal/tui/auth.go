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

	p := textinput.New()
	p.Placeholder = "password"
	p.Prompt = ""
	p.EchoMode = textinput.EchoPassword
	p.CharLimit = 256

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
	// ── Logo + tagline ────────────────────────────────────────────────────
	logo := styleLogo.Render(tetherLogo)
	tagline := styleMuted.Render("your personal AI assistant")

	// ── Form ──────────────────────────────────────────────────────────────
	label := styleMuted.Render
	var submitLabel string
	if m.mode == authModeLogin {
		submitLabel = "  log in  "
	} else {
		submitLabel = "  create  "
	}

	formInner := lipgloss.JoinVertical(lipgloss.Left,
		label("user")+"  "+m.username.View(),
		"",
		label("pass")+"  "+m.password.View(),
		"",
		styleTabActive.Render(submitLabel),
	)

	status := ""
	if m.statusText != "" {
		if m.statusErr {
			status = styleError.Render(m.statusText)
		} else {
			status = styleInfo.Render(m.statusText)
		}
		formInner = lipgloss.JoinVertical(lipgloss.Left, formInner, "", status)
	}

	box := styleAuthBox.Render(formInner)

	// ── Hint ──────────────────────────────────────────────────────────────
	hint := styleDim.Render("tab · switch   enter · submit   q · quit")

	// ── Stack all pieces, centered horizontally ───────────────────────────
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

func (m authModel) cursor() *tea.Cursor {
	if m.focused == 0 {
		return m.username.Cursor()
	}
	return m.password.Cursor()
}
