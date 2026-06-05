package tui

import (
	"strings"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

type changelogModalModel struct {
	open            bool
	title           string
	body            string
	markSeenOnClose bool
	width           int
	height          int
	viewport        viewport.Model
}

func newChangelogModalModel() changelogModalModel {
	vp := viewport.New(viewport.WithWidth(80), viewport.WithHeight(10))
	vp.KeyMap.Left.SetEnabled(false)
	vp.KeyMap.Right.SetEnabled(false)
	return changelogModalModel{viewport: vp}
}

func (m changelogModalModel) withSize(w, h int) changelogModalModel {
	m.width = w
	m.height = h
	return m.reflow()
}

func (m changelogModalModel) openModal(title, body string, markSeenOnClose bool) changelogModalModel {
	m.open = true
	m.title = strings.TrimSpace(title)
	m.body = strings.TrimSpace(body)
	m.markSeenOnClose = markSeenOnClose
	m.viewport.GotoTop()
	return m.reflow()
}

func (m changelogModalModel) closeModal() (changelogModalModel, bool) {
	shouldMark := m.open && m.markSeenOnClose
	m.open = false
	m.title = ""
	m.body = ""
	m.markSeenOnClose = false
	m.viewport.SetContent("")
	return m, shouldMark
}

func (m changelogModalModel) Update(msg tea.Msg) (changelogModalModel, tea.Cmd, bool) {
	if !m.open {
		return m, nil, false
	}
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch msg.String() {
		case "esc", "q", "enter":
			var mark bool
			m, mark = m.closeModal()
			return m, nil, mark
		}
	case tea.WindowSizeMsg:
		m = m.withSize(msg.Width, msg.Height-1)
		return m, nil, false
	}
	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	return m, cmd, false
}

func (m changelogModalModel) View() tea.View {
	if !m.open {
		return tea.NewView("")
	}
	if m.width <= 0 || m.height <= 0 {
		return tea.NewView("")
	}
	modalW, _ := m.modalSize()
	innerW := max(20, modalW-styleChangelogModal.GetHorizontalFrameSize())

	title := styleChangelogTitle.Width(innerW).Render(m.title)
	rule := styleChangelogRule.Width(innerW).Render(strings.Repeat("-", innerW))
	footer := styleChangelogFooter.Width(innerW).Render("enter close  |  esc close  |  up/down scroll")
	body := lipgloss.JoinVertical(lipgloss.Left, title, rule, m.viewport.View(), footer)
	modal := styleChangelogModal.Width(innerW).Render(body)

	ws := lipgloss.WithWhitespaceStyle(lipgloss.NewStyle().Background(colorBg))
	return tea.NewView(lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, modal, ws))
}

func (m changelogModalModel) reflow() changelogModalModel {
	if !m.open || m.width <= 0 || m.height <= 0 {
		return m
	}
	modalW, modalH := m.modalSize()
	innerW := max(20, modalW-styleChangelogModal.GetHorizontalFrameSize())
	contentH := max(4, modalH-styleChangelogModal.GetVerticalFrameSize()-3)
	m.viewport.SetWidth(innerW)
	m.viewport.SetHeight(contentH)
	rendered := renderRichText(m.body, innerW, richTextSystem)
	setViewportContent(&m.viewport, rendered, colorBotMsgBg)
	return m
}

func (m changelogModalModel) modalSize() (int, int) {
	modalW := min(max(32, m.width-4), 100)
	modalH := min(max(10, m.height-4), 32)
	return modalW, modalH
}
