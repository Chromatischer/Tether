package tui

import (
	"math"
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

	// hit is a heap-allocated struct shared across copies of authModel
	// (value receivers). View() mutates its fields so Update() can read the
	// current layout without recomputing it.
	hit *authHitRegions
}

// authHitRegions records the absolute terminal coordinates of clickable
// regions on the auth screen. Populated by View() on every render.
type authHitRegions struct {
	modeY     int // Y of the Login / Sign up tab row
	loginX0   int // X where the Login tab starts
	loginX1   int // X where the Login tab ends (and Sign up begins)
	signupX1  int // X where the Sign up tab ends
	usernameY int
	passwordY int
	submitY   int
	formX0    int // X of the form's left edge (inside the box)
	formX1    int // X of the form's right edge (exclusive)
}

type authSubmitMsg struct {
	Mode     authMode
	Username string
	Password string
}

type authSwitchModeMsg struct {
	Mode authMode
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
		hit:      &authHitRegions{},
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
		if msg.Button != tea.MouseLeft || m.hit == nil {
			break
		}
		h := m.hit
		// Mode tabs (Login / Sign up)
		if msg.Y == h.modeY {
			if msg.X >= h.loginX0 && msg.X < h.loginX1 {
				return m, func() tea.Msg { return authSwitchModeMsg{Mode: authModeLogin} }
			}
			if msg.X >= h.loginX1 && msg.X < h.signupX1 {
				return m, func() tea.Msg { return authSwitchModeMsg{Mode: authModeSignup} }
			}
		}
		// Field rows — allow clicking anywhere in the form's X range.
		if msg.X >= h.formX0 && msg.X < h.formX1 {
			switch msg.Y {
			case h.usernameY:
				m.focused = 0
				m.username.Focus()
				m.password.Blur()
				return m, nil
			case h.passwordY:
				m.focused = 1
				m.password.Focus()
				m.username.Blur()
				return m, nil
			case h.submitY:
				return m, func() tea.Msg {
					return authSubmitMsg{Mode: m.mode, Username: strings.TrimSpace(m.username.Value()), Password: m.password.Value()}
				}
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
	logo := styleLogo.Render(renderBrandLogo(tetherLogo, colorBg))
	tagline := styleAuthTagline.Render("personal AI over SSH")

	// ── Mode header (tab switcher inside the box) ─────────────────────────
	var loginLabel, signupLabel string
	if m.mode == authModeLogin {
		loginLabel = styleAuthModeHeaderActive.Render("▸ Login")
		signupLabel = styleAuthModeHeader.Render("Sign up")
	} else {
		loginLabel = styleAuthModeHeader.Render("Login")
		signupLabel = styleAuthModeHeaderActive.Render("▸ Sign up")
	}
	loginLabelW := lipgloss.Width(loginLabel)
	signupLabelW := lipgloss.Width(signupLabel)
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
	// every other row to match so the box has a uniform interior bg.
	formWidth := max(lipgloss.Width(usernameRow), lipgloss.Width(passwordRow))
	modeRow = styleAuthRow.Width(formWidth).Render(modeRow)
	usernameRow = styleAuthRow.Width(formWidth).Render(usernameRow)
	passwordRow = styleAuthRow.Width(formWidth).Render(passwordRow)
	divider := styleAuthDivider.Width(formWidth).Render(strings.Repeat("─", formWidth))
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
			statusLine = styleAuthStatusErr.Render("✗ " + m.statusText)
		} else {
			statusLine = styleAuthStatusOK.Render("✓ " + m.statusText)
		}
		spacer := styleAuthRow.Width(formWidth).Render("")
		statusLine = styleAuthRow.Width(formWidth).Render(statusLine)
		formInner = lipgloss.JoinVertical(lipgloss.Left, formInner, spacer, statusLine)
	}

	box := styleAuthBox.Render(formInner)

	// ── Hint ──────────────────────────────────────────────────────────────
	hint := styleAuthHint.Render("tab · switch field   enter · submit   ctrl+c · quit")

	// ── Stack centered ────────────────────────────────────────────────────
	// Each child is pre-padded to a uniform `blockWidth` with bg=colorBg so
	// that JoinVertical doesn't insert un-styled spaces beside narrower
	// elements (which would render as terminal-default cells after each
	// child's ANSI reset).
	bgStyle := lipgloss.NewStyle().Background(colorBg)
	wsOpt := lipgloss.WithWhitespaceStyle(bgStyle)

	blockWidth := lipgloss.Width(logo)
	for _, w := range []int{lipgloss.Width(tagline), lipgloss.Width(box), lipgloss.Width(hint)} {
		if w > blockWidth {
			blockWidth = w
		}
	}
	pad := func(s string) string {
		return lipgloss.PlaceHorizontal(blockWidth, lipgloss.Center, s, wsOpt)
	}
	emptyRow := bgStyle.Width(blockWidth).Render("")

	block := lipgloss.JoinVertical(lipgloss.Left,
		pad(logo),
		pad(tagline),
		emptyRow,
		pad(box),
		emptyRow,
		pad(hint),
	)

	// ── Hit regions ───────────────────────────────────────────────────────
	// Compute absolute terminal coordinates of clickable rows/tabs so
	// Update() can route MouseClickMsg without re-deriving the layout.
	// The body starts at row 1 (app.go adds a 1-row header above it), then
	// the block is centered vertically in (m.height) body rows and
	// horizontally in (m.width) columns. Within the block, the box is
	// centered horizontally in blockWidth (via PlaceHorizontal(Center)).
	if m.hit != nil && m.width > 0 && m.height > 0 {
		boxWidth := lipgloss.Width(box)
		blockHeight := lipgloss.Height(block)

		centerOffset := func(total, content int) int {
			gap := total - content
			if gap <= 0 {
				return 0
			}
			split := int(math.Round(float64(gap) * 0.5))
			return gap - split
		}
		topGap := centerOffset(m.height, blockHeight)
		outerLeft := centerOffset(m.width, blockWidth)
		blockLeft := centerOffset(blockWidth, boxWidth)
		boxAbsLeft := outerLeft + blockLeft

		// Block layout (0-indexed, relative to block top):
		//   0..5   logo (6 rows)
		//   6      tagline
		//   7      empty row
		//   8..    box (top border, then inner rows, then bottom border)
		// Inside the box, after the top border (row 8+1):
		//   9  modeRow   10 divider   11 username   12 password
		//  13 divider   14 submit
		//   (+2 if status: spacer + statusLine)
		baseY := 1 + topGap // +1 for app header row
		boxTop := baseY + 8
		m.hit.modeY = boxTop + 1
		m.hit.usernameY = boxTop + 3
		m.hit.passwordY = boxTop + 4
		m.hit.submitY = boxTop + 6

		// Form content starts at boxAbsLeft + 1 (inside the box border).
		m.hit.formX0 = boxAbsLeft + 1
		m.hit.formX1 = m.hit.formX0 + formWidth

		// Mode tabs are flush-left inside the form; each tab's X range is
		// its own rendered width.
		m.hit.loginX0 = m.hit.formX0
		m.hit.loginX1 = m.hit.formX0 + loginLabelW
		m.hit.signupX1 = m.hit.loginX1 + signupLabelW
	}

	if m.width > 0 && m.height > 0 {
		placed := lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, block, wsOpt)
		return tea.NewView(placed)
	}
	if m.width > 0 {
		placed := lipgloss.PlaceHorizontal(m.width, lipgloss.Center, block, wsOpt)
		return tea.NewView(placed)
	}
	return tea.NewView(block)
}

func (m authModel) cursor() *tea.Cursor {
	if m.focused == 0 {
		return m.username.Cursor()
	}
	return m.password.Cursor()
}
