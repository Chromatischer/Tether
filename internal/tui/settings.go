package tui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"tether/internal/store"
)

type settingsSection int

const (
	settingsSectionRules settingsSection = iota
	settingsSectionConfirm
	settingsSectionSignal
	settingsSectionRetention
)

var settingsSectionLabels = []string{"proactive rules", "confirmation", "signal", "retention"}

type settingsModel struct {
	ctx    *SessionContext
	userID int64
	sec    settingsSection
	w, h   int

	// proactive rules section
	rulesVP      viewport.Model
	rulesTA      textarea.Model
	rulesEditing bool
	rulesContent string // loaded YAML

	// confirmation strictness section
	confirmVP   viewport.Model
	confirmSel  int // 0=ask, 1=always, 2=never
	confirmOpts []string

	// signal section
	signalVP        viewport.Model
	signalLinked    bool
	signalNumber    string
	signalLinkCode  string
	signalLinkInput textinput.Model
	signalInputMode bool // waiting for user to type code

	// retention section
	retentionVP      viewport.Model
	retentionInput   textinput.Model
	retentionDays    int
	retentionInput2  textinput.Model // memory retention days
	retentionMemDays int
	retentionFocus   int // 0=chat, 1=memory

	status    string
	statusErr bool
}

// settingsLoadedMsg carries all settings data loaded asynchronously.
type settingsLoadedMsg struct {
	rulesYAML         string
	confirmStrictness string
	signalLinked      bool
	signalNumber      string
	retentionDays     int
	retentionMemDays  int
	err               error
}

// settingsSignalLinkMsg is dispatched when a link code is generated.
type settingsSignalLinkMsg struct {
	code string
	err  error
}

func newSettingsModel(ctx *SessionContext) settingsModel {
	mk := func() viewport.Model {
		vp := viewport.New(viewport.WithWidth(80), viewport.WithHeight(10))
		vp.KeyMap.Left.SetEnabled(false)
		vp.KeyMap.Right.SetEnabled(false)
		return vp
	}

	ta := textarea.New()
	ta.Placeholder = "# YAML rules…"
	ta.CharLimit = 8000

	li := textinput.New()
	li.Prompt = "link code: "
	li.CharLimit = 64

	ri := textinput.New()
	ri.Prompt = "chat retention days (0=forever): "
	ri.CharLimit = 6

	ri2 := textinput.New()
	ri2.Prompt = "memory retention days (0=forever): "
	ri2.CharLimit = 6

	return settingsModel{
		ctx:             ctx,
		rulesVP:         mk(),
		confirmVP:       mk(),
		signalVP:        mk(),
		retentionVP:     mk(),
		rulesTA:         ta,
		confirmOpts:     []string{"ask", "always", "never"},
		signalLinkInput: li,
		retentionInput:  ri,
		retentionInput2: ri2,
	}
}

func (m settingsModel) withUser(userID int64) settingsModel {
	m.userID = userID
	return m
}

func (m settingsModel) withSize(w, h int) settingsModel {
	if w <= 0 || h <= 0 {
		return m
	}
	m.w, m.h = w, h
	ch := max(1, h-1) // 1 line for section tab bar
	m.rulesVP.SetWidth(w)
	m.rulesVP.SetHeight(ch)
	m.confirmVP.SetWidth(w)
	m.confirmVP.SetHeight(ch)
	m.signalVP.SetWidth(w)
	m.signalVP.SetHeight(ch)
	m.retentionVP.SetWidth(w)
	m.retentionVP.SetHeight(ch)
	m.rulesTA.SetWidth(max(20, w-4))
	m.rulesTA.SetHeight(max(5, ch-4))
	m.signalLinkInput.SetWidth(max(20, w-4))
	m.retentionInput.SetWidth(max(20, w-4))
	m.retentionInput2.SetWidth(max(20, w-4))
	m.rebuildSections()
	return m
}

func (m settingsModel) loadCmd() tea.Cmd {
	ctx := m.ctx
	userID := m.userID
	return func() tea.Msg {
		if ctx == nil || userID == 0 {
			return settingsLoadedMsg{}
		}

		yaml, _, _, err := store.GetProactiveRulesYAML(ctx.DB, userID)
		if err != nil {
			return settingsLoadedMsg{err: err}
		}

		confirm, _, _ := store.GetUserSetting(ctx.DB, userID, "confirm_strictness")
		if confirm == "" {
			confirm = "ask"
		}

		signalNum, signalLinked, _ := store.GetSignalNumber(ctx.DB, userID)

		chatDays := 0
		if v, ok, _ := store.GetUserSetting(ctx.DB, userID, "retention_chat_days"); ok {
			chatDays, _ = strconv.Atoi(v)
		}
		memDays := 0
		if v, ok, _ := store.GetUserSetting(ctx.DB, userID, "retention_memory_days"); ok {
			memDays, _ = strconv.Atoi(v)
		}

		return settingsLoadedMsg{
			rulesYAML:         yaml,
			confirmStrictness: confirm,
			signalLinked:      signalLinked,
			signalNumber:      signalNum,
			retentionDays:     chatDays,
			retentionMemDays:  memDays,
		}
	}
}

func (m settingsModel) Update(msg tea.Msg) (settingsModel, tea.Cmd) {
	switch msg := msg.(type) {

	case settingsLoadedMsg:
		if msg.err != nil {
			m.status = "load error: " + msg.err.Error()
			m.statusErr = true
			m.rebuildSections()
			return m, nil
		}
		m.rulesContent = msg.rulesYAML
		// map confirm string → index
		m.confirmSel = 0
		for i, opt := range m.confirmOpts {
			if opt == msg.confirmStrictness {
				m.confirmSel = i
			}
		}
		m.signalLinked = msg.signalLinked
		m.signalNumber = msg.signalNumber
		m.retentionDays = msg.retentionDays
		m.retentionMemDays = msg.retentionMemDays
		m.status = ""
		m.statusErr = false
		m.rebuildSections()
		return m, nil

	case settingsSignalLinkMsg:
		if msg.err != nil {
			m.status = "error: " + msg.err.Error()
			m.statusErr = true
		} else {
			m.signalLinkCode = msg.code
			m.status = ""
		}
		m.rebuildSections()
		return m, nil

	case tea.KeyPressMsg:
		// Section-switching (always available unless sub-mode active)
		if !m.rulesEditing && !m.signalInputMode {
			switch msg.String() {
			case "1":
				return m.switchSection(settingsSectionRules)
			case "2":
				return m.switchSection(settingsSectionConfirm)
			case "3":
				return m.switchSection(settingsSectionSignal)
			case "4":
				return m.switchSection(settingsSectionRetention)
			case "[":
				next := (int(m.sec) - 1 + len(settingsSectionLabels)) % len(settingsSectionLabels)
				return m.switchSection(settingsSection(next))
			case "]":
				next := (int(m.sec) + 1) % len(settingsSectionLabels)
				return m.switchSection(settingsSection(next))
			case "r":
				return m, m.loadCmd()
			}
		}

		switch m.sec {
		case settingsSectionRules:
			return m.updateRules(msg)
		case settingsSectionConfirm:
			return m.updateConfirm(msg)
		case settingsSectionSignal:
			return m.updateSignal(msg)
		case settingsSectionRetention:
			return m.updateRetention(msg)
		}

	case tea.MouseClickMsg:
		if msg.Button == tea.MouseLeft && msg.Y == 1 {
			if sec, ok := m.hitSection(msg.X); ok {
				return m.switchSection(sec)
			}
		}
	}

	// Pass through to active viewport for scrolling.
	var cmd tea.Cmd
	if !m.rulesEditing {
		switch m.sec {
		case settingsSectionRules:
			m.rulesVP, cmd = m.rulesVP.Update(msg)
		case settingsSectionConfirm:
			m.confirmVP, cmd = m.confirmVP.Update(msg)
		case settingsSectionSignal:
			m.signalVP, cmd = m.signalVP.Update(msg)
		case settingsSectionRetention:
			m.retentionVP, cmd = m.retentionVP.Update(msg)
		}
	}
	return m, cmd
}

func (m settingsModel) updateRules(msg tea.KeyPressMsg) (settingsModel, tea.Cmd) {
	if m.rulesEditing {
		switch msg.String() {
		case "ctrl+s":
			m.rulesEditing = false
			yaml := m.rulesTA.Value()
			if err := store.SetProactiveRulesYAML(m.ctx.DB, m.userID, yaml); err != nil {
				m.status = "save error: " + err.Error()
				m.statusErr = true
			} else {
				m.rulesContent = yaml
				m.status = "rules saved"
				m.statusErr = false
			}
			m.rebuildSections()
			return m, nil
		case "esc":
			m.rulesEditing = false
			m.rebuildSections()
			return m, nil
		}
		var cmd tea.Cmd
		m.rulesTA, cmd = m.rulesTA.Update(msg)
		return m, cmd
	}

	switch msg.String() {
	case "e":
		m.rulesEditing = true
		m.rulesTA.SetValue(m.rulesContent)
		m.rulesTA.Focus()
		m.rebuildSections()
	}
	return m, nil
}

func (m settingsModel) updateConfirm(msg tea.KeyPressMsg) (settingsModel, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		if m.confirmSel > 0 {
			m.confirmSel--
			m.rebuildSections()
		}
	case "down", "j":
		if m.confirmSel < len(m.confirmOpts)-1 {
			m.confirmSel++
			m.rebuildSections()
		}
	case "enter", " ":
		chosen := m.confirmOpts[m.confirmSel]
		if err := store.SetUserSetting(m.ctx.DB, m.userID, "confirm_strictness", chosen); err != nil {
			m.status = "save error: " + err.Error()
			m.statusErr = true
		} else {
			m.status = "saved: " + chosen
			m.statusErr = false
		}
		m.rebuildSections()
	}
	return m, nil
}

func (m settingsModel) updateSignal(msg tea.KeyPressMsg) (settingsModel, tea.Cmd) {
	if m.signalInputMode {
		switch msg.String() {
		case "esc":
			m.signalInputMode = false
			m.rebuildSections()
			return m, nil
		case "enter":
			// no-op — link code is generated server-side; input just shows user the code
			m.signalInputMode = false
			m.rebuildSections()
			return m, nil
		}
		var cmd tea.Cmd
		m.signalLinkInput, cmd = m.signalLinkInput.Update(msg)
		return m, cmd
	}

	switch msg.String() {
	case "l":
		// Generate a new link code
		if !m.ctx.Config.Signal.Enabled {
			m.status = "signal not enabled in config"
			m.statusErr = true
			m.rebuildSections()
			return m, nil
		}
		return m, m.generateLinkCodeCmd()
	case "u":
		if m.signalLinked {
			if err := store.UnlinkSignalNumber(m.ctx.DB, m.userID); err != nil {
				m.status = "unlink error: " + err.Error()
				m.statusErr = true
			} else {
				m.signalLinked = false
				m.signalNumber = ""
				m.signalLinkCode = ""
				m.status = "signal unlinked"
				m.statusErr = false
			}
			m.rebuildSections()
		}
	}
	return m, nil
}

func (m settingsModel) generateLinkCodeCmd() tea.Cmd {
	ctx := m.ctx
	userID := m.userID
	return func() tea.Msg {
		code, err := store.CreateSignalLinkCode(ctx.DB, userID, 10*time.Minute)
		return settingsSignalLinkMsg{code: code, err: err}
	}
}

func (m settingsModel) updateRetention(msg tea.KeyPressMsg) (settingsModel, tea.Cmd) {
	// Focus switching between the two inputs
	switch msg.String() {
	case "tab":
		m.retentionFocus = (m.retentionFocus + 1) % 2
		if m.retentionFocus == 0 {
			m.retentionInput.Focus()
			m.retentionInput2.Blur()
		} else {
			m.retentionInput.Blur()
			m.retentionInput2.Focus()
		}
		m.rebuildSections()
		return m, nil
	case "enter":
		if m.retentionFocus == 0 {
			v := strings.TrimSpace(m.retentionInput.Value())
			days, err := strconv.Atoi(v)
			if err != nil || days < 0 {
				m.status = "invalid number"
				m.statusErr = true
				m.rebuildSections()
				return m, nil
			}
			if err := store.SetUserSetting(m.ctx.DB, m.userID, "retention_chat_days", strconv.Itoa(days)); err != nil {
				m.status = "save error: " + err.Error()
				m.statusErr = true
			} else {
				m.retentionDays = days
				m.status = "chat retention saved"
				m.statusErr = false
			}
		} else {
			v := strings.TrimSpace(m.retentionInput2.Value())
			days, err := strconv.Atoi(v)
			if err != nil || days < 0 {
				m.status = "invalid number"
				m.statusErr = true
				m.rebuildSections()
				return m, nil
			}
			if err := store.SetUserSetting(m.ctx.DB, m.userID, "retention_memory_days", strconv.Itoa(days)); err != nil {
				m.status = "save error: " + err.Error()
				m.statusErr = true
			} else {
				m.retentionMemDays = days
				m.status = "memory retention saved"
				m.statusErr = false
			}
		}
		m.rebuildSections()
		return m, nil
	}

	var cmd tea.Cmd
	if m.retentionFocus == 0 {
		m.retentionInput, cmd = m.retentionInput.Update(msg)
	} else {
		m.retentionInput2, cmd = m.retentionInput2.Update(msg)
	}
	return m, cmd
}

func (m settingsModel) switchSection(sec settingsSection) (settingsModel, tea.Cmd) {
	m.rulesEditing = false
	m.signalInputMode = false
	m.sec = sec
	// Prime retention inputs when entering that section
	if sec == settingsSectionRetention {
		if m.retentionInput.Value() == "" {
			m.retentionInput.SetValue(strconv.Itoa(m.retentionDays))
		}
		if m.retentionInput2.Value() == "" {
			m.retentionInput2.SetValue(strconv.Itoa(m.retentionMemDays))
		}
		m.retentionInput.Focus()
		m.retentionInput2.Blur()
		m.retentionFocus = 0
	}
	m.rebuildSections()
	return m, nil
}

func (m *settingsModel) rebuildSections() {
	m.rebuildRules()
	m.rebuildConfirm()
	m.rebuildSignal()
	m.rebuildRetention()
}

func (m *settingsModel) rebuildRules() {
	if m.rulesVP.Width() <= 0 {
		return
	}
	if m.rulesEditing {
		// When editing, the textarea is rendered directly in View()
		return
	}
	var b strings.Builder
	b.WriteString(styleTitle.Render("proactive rules") + "\n\n")
	if m.rulesContent == "" {
		b.WriteString(styleDim.Render("(no rules loaded)") + "\n")
	} else {
		for _, line := range strings.Split(m.rulesContent, "\n") {
			b.WriteString(styleMuted.Render(line) + "\n")
		}
	}
	b.WriteString("\n" + m.hintLine(settingsSectionRules))
	m.rulesVP.SetContent(strings.TrimRight(b.String(), "\n"))
	m.rulesVP.GotoTop()
}

func (m *settingsModel) rebuildConfirm() {
	if m.confirmVP.Width() <= 0 {
		return
	}
	var b strings.Builder
	b.WriteString(styleTitle.Render("confirmation strictness") + "\n\n")
	b.WriteString(styleMuted.Render("When should Tether ask for confirmation before acting?") + "\n\n")
	for i, opt := range m.confirmOpts {
		if i == m.confirmSel {
			b.WriteString(styleAccent.Render("▶ "+opt) + "\n")
		} else {
			b.WriteString(styleDim.Render("  "+opt) + "\n")
		}
	}
	b.WriteString("\n" + styleDim.Render("ask") + " — prompt before tool calls that look risky\n")
	b.WriteString(styleDim.Render("always") + " — always prompt, even for safe operations\n")
	b.WriteString(styleDim.Render("never") + " — never prompt (use with caution)\n")
	b.WriteString("\n" + m.hintLine(settingsSectionConfirm))
	m.confirmVP.SetContent(strings.TrimRight(b.String(), "\n"))
}

func (m *settingsModel) rebuildSignal() {
	if m.signalVP.Width() <= 0 {
		return
	}
	var b strings.Builder
	b.WriteString(styleTitle.Render("signal") + "\n\n")
	if !m.ctx.Config.Signal.Enabled {
		b.WriteString(styleDim.Render("Signal integration is disabled in server config.") + "\n")
		m.signalVP.SetContent(strings.TrimRight(b.String(), "\n"))
		return
	}
	if m.signalLinked {
		b.WriteString(styleInfo.Render("linked") + "  " + styleMuted.Render(m.signalNumber) + "\n\n")
		b.WriteString(styleDim.Render("u · unlink") + "\n")
	} else {
		b.WriteString(styleDim.Render("not linked") + "\n\n")
		b.WriteString(styleDim.Render("l · generate link code") + "\n")
	}
	if m.signalLinkCode != "" {
		b.WriteString("\n" + styleMuted.Render("send this code to the Tether bot:") + "\n")
		b.WriteString(styleAccent.Render("  /link "+m.signalLinkCode) + "\n")
		b.WriteString(styleDim.Render(fmt.Sprintf("  (expires in 10 min)")) + "\n")
	}
	b.WriteString("\n" + m.hintLine(settingsSectionSignal))
	m.signalVP.SetContent(strings.TrimRight(b.String(), "\n"))
}

func (m *settingsModel) rebuildRetention() {
	if m.retentionVP.Width() <= 0 {
		return
	}
	chatLabel := styleMuted.Render("chat history")
	memLabel := styleMuted.Render("memory items")
	chatVal := "forever"
	if m.retentionDays > 0 {
		chatVal = fmt.Sprintf("%d days", m.retentionDays)
	}
	memVal := "forever"
	if m.retentionMemDays > 0 {
		memVal = fmt.Sprintf("%d days", m.retentionMemDays)
	}

	var b strings.Builder
	b.WriteString(styleTitle.Render("retention") + "\n\n")
	b.WriteString(chatLabel + "  " + styleDim.Render("(current: "+chatVal+")") + "\n")
	b.WriteString(m.retentionInput.View() + "\n\n")
	b.WriteString(memLabel + "  " + styleDim.Render("(current: "+memVal+")") + "\n")
	b.WriteString(m.retentionInput2.View() + "\n\n")
	b.WriteString(m.hintLine(settingsSectionRetention))
	m.retentionVP.SetContent(strings.TrimRight(b.String(), "\n"))
}

func (m settingsModel) hintLine(sec settingsSection) string {
	base := styleDim.Render("[/] sections  r·refresh")
	extra := ""
	switch sec {
	case settingsSectionRules:
		extra = "  " + styleDim.Render("e·edit")
	case settingsSectionConfirm:
		extra = "  " + styleDim.Render("↑↓·select  enter·save")
	case settingsSectionSignal:
		if m.ctx.Config.Signal.Enabled {
			if m.signalLinked {
				extra = "  " + styleDim.Render("u·unlink")
			} else {
				extra = "  " + styleDim.Render("l·link code")
			}
		}
	case settingsSectionRetention:
		extra = "  " + styleDim.Render("tab·switch  enter·save")
	}
	if m.status != "" {
		if m.statusErr {
			return styleError.Render(m.status) + "  " + base + extra
		}
		return styleInfo.Render(m.status) + "  " + base + extra
	}
	return base + extra
}

func (m settingsModel) sectionButtons() []headerButton {
	btns := make([]headerButton, len(settingsSectionLabels))
	curX := 0
	for i, label := range settingsSectionLabels {
		rendered := styleTab.Render(label)
		if settingsSection(i) == m.sec {
			rendered = styleTabActive.Render(label)
		}
		w := lipgloss.Width(rendered)
		btns[i] = headerButton{ID: label, Label: label, X0: curX, X1: curX + w}
		curX += w
	}
	return btns
}

func (m settingsModel) hitSection(x int) (settingsSection, bool) {
	for i, b := range m.sectionButtons() {
		if x >= b.X0 && x < b.X1 {
			return settingsSection(i), true
		}
	}
	return settingsSectionRules, false
}

func (m settingsModel) View() tea.View {
	tabs := make([]string, len(settingsSectionLabels))
	for i, label := range settingsSectionLabels {
		if settingsSection(i) == m.sec {
			tabs[i] = styleTabActive.Render(label)
		} else {
			tabs[i] = styleTab.Render(label)
		}
	}
	tabBar := lipgloss.JoinHorizontal(lipgloss.Top, tabs...)
	if m.w > 0 {
		if gap := m.w - lipgloss.Width(tabBar); gap > 0 {
			tabBar += styleHeaderSpacer.Render(strings.Repeat(" ", gap))
		}
	}

	var body string
	switch m.sec {
	case settingsSectionRules:
		if m.rulesEditing {
			hint := styleDim.Render("ctrl+s·save  esc·cancel")
			body = m.rulesTA.View() + "\n" + hint
		} else {
			body = m.rulesVP.View()
		}
	case settingsSectionConfirm:
		body = m.confirmVP.View()
	case settingsSectionSignal:
		body = m.signalVP.View()
	case settingsSectionRetention:
		body = m.retentionVP.View()
	}
	if m.w > 0 {
		body = lipgloss.NewStyle().Background(colorBg).Width(m.w).Render(body)
	}

	return tea.NewView(tabBar + "\n" + body)
}
