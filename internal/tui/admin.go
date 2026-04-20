package tui

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"tether/internal/config"
	"tether/internal/secrets"
	"tether/internal/store"
)

type adminTab int

const (
	adminTabAudit adminTab = iota
	adminTabUsers
	adminTabJobs
	adminTabSignal
	adminTabSetup
)

var adminTabLabels = []string{"audit", "users", "jobs", "signal", "setup"}

type adminModel struct {
	ctx  *SessionContext
	tab  adminTab
	w, h int

	audit  viewport.Model
	users  viewport.Model
	jobs   viewport.Model
	signal viewport.Model
	setup  viewport.Model

	userList []store.User
	userSel  int

	setupPath       string
	setupOpenRouter textinput.Model
	setupDiscord    textinput.Model
	setupSignal     textinput.Model
	setupMasterKey  textinput.Model
	setupFocus      int
	setupStatus     string
	setupStatusErr  bool
}

type adminLoadMsg struct {
	tab     adminTab
	content string
	users   []store.User
	env     config.AdminEnv
	err     error
}

type adminSetupSavedMsg struct {
	err    error
	status string
}

func newAdminModel(ctx *SessionContext) adminModel {
	mk := func() viewport.Model {
		vp := viewport.New(viewport.WithWidth(80), viewport.WithHeight(10))
		vp.KeyMap.Left.SetEnabled(false)
		vp.KeyMap.Right.SetEnabled(false)
		vp.Style = lipgloss.NewStyle().Background(colorBg)
		return vp
	}
	masked := func(prompt string) textinput.Model {
		ti := textinput.New()
		ti.Prompt = prompt
		ti.CharLimit = 512
		ti.EchoMode = textinput.EchoPassword
		return ti
	}
	plain := func(prompt string) textinput.Model {
		ti := textinput.New()
		ti.Prompt = prompt
		ti.CharLimit = 256
		return ti
	}

	m := adminModel{
		ctx:             ctx,
		audit:           mk(),
		users:           mk(),
		jobs:            mk(),
		signal:          mk(),
		setup:           mk(),
		setupPath:       config.AdminEnvPath(ctx.Config.Paths.DataDir),
		setupOpenRouter: masked("OpenRouter API key: "),
		setupDiscord:    masked("Discord bot token: "),
		setupSignal:     plain("Signal account number: "),
		setupMasterKey:  masked("Secrets master key: "),
	}
	m.setSetupFocus(0)
	return m
}

func (m adminModel) withSize(w, h int) adminModel {
	if w <= 0 || h <= 0 {
		return m
	}
	m.w, m.h = w, h
	ch := max(1, h-1)
	m.audit.SetWidth(w)
	m.audit.SetHeight(ch)
	m.users.SetWidth(w)
	m.users.SetHeight(ch)
	m.jobs.SetWidth(w)
	m.jobs.SetHeight(ch)
	m.signal.SetWidth(w)
	m.signal.SetHeight(ch)
	m.setup.SetWidth(w)
	m.setup.SetHeight(ch)
	inputW := max(24, w-6)
	m.setupOpenRouter.SetWidth(inputW)
	m.setupDiscord.SetWidth(inputW)
	m.setupSignal.SetWidth(inputW)
	m.setupMasterKey.SetWidth(inputW)
	return m
}

func (m adminModel) loadCmd() tea.Cmd {
	return m.loadTabCmd(m.tab)
}

func (m adminModel) loadTabCmd(tab adminTab) tea.Cmd {
	ctx := m.ctx
	return func() tea.Msg {
		switch tab {
		case adminTabAudit:
			evs, err := store.ListAuditEvents(ctx.DB, 100)
			if err != nil {
				return adminLoadMsg{tab: tab, err: err}
			}
			var b strings.Builder
			for _, ev := range evs {
				ts := ev.CreatedAt.UTC().Format("01-02 15:04:05")
				line := styleDimBg.Render(ts) + styleBodyBg.Render("  ") + styleAccentBg.Render(ev.Type)
				if ev.UserID != nil {
					line += styleBodyBg.Render("  ") + styleDimBg.Render(fmt.Sprintf("user=%d", *ev.UserID))
				}
				if ev.Payload != "" {
					line += styleBodyBg.Render("  ") + styleMutedBg.Render(ev.Payload)
				}
				b.WriteString(line + "\n")
			}
			return adminLoadMsg{tab: tab, content: strings.TrimRight(b.String(), "\n")}

		case adminTabUsers:
			users, err := store.ListUsers(ctx.DB)
			if err != nil {
				return adminLoadMsg{tab: tab, err: err}
			}
			return adminLoadMsg{tab: tab, users: users}

		case adminTabJobs:
			lastTick, ok, err := store.LatestAuditEventTime(ctx.DB, "proactive_tick")
			lt := "(never)"
			if err == nil && ok {
				lt = lastTick.UTC().Format(time.RFC3339)
			}
			undelivered, _ := store.CountUndeliveredNotifications(ctx.DB)
			content := styleTitleBg.Render("jobs") + "\n\n" +
				styleMutedBg.Render("proactive_tick last run") + "\n" + styleMutedBg.Render("  "+lt) + "\n\n" +
				styleMutedBg.Render("undelivered notifications") + "\n" + styleMutedBg.Render(fmt.Sprintf("  %d", undelivered)) + "\n\n" +
				styleDimBg.Render("r · refresh")
			return adminLoadMsg{tab: tab, content: content}

		case adminTabSignal:
			if !ctx.Config.Signal.Enabled {
				return adminLoadMsg{tab: tab, content: styleTitleBg.Render("signal") + "\n\n" + styleDimBg.Render("disabled in config")}
			}
			addr := ctx.Config.Signal.HTTPAddr
			base := addr
			if !strings.HasPrefix(base, "http") {
				base = "http://" + base
			}
			checkLine := styleDimBg.Render("(checking…)")
			if req, err := http.NewRequest(http.MethodGet, base+"/api/v1/check", nil); err == nil {
				if resp, err := http.DefaultClient.Do(req); err != nil {
					checkLine = styleErrorBg.Render("check failed: " + err.Error())
				} else {
					resp.Body.Close()
					checkLine = styleInfoBg.Render(fmt.Sprintf("HTTP %d", resp.StatusCode))
				}
			}
			content := styleTitleBg.Render("signal") + "\n\n" +
				styleMutedBg.Render("account") + "\n" + styleMutedBg.Render("  "+ctx.Config.Signal.AccountNumber) + "\n\n" +
				styleMutedBg.Render("status") + "\n" + styleBodyBg.Render("  ") + checkLine + "\n\n" +
				styleDimBg.Render("r · refresh")
			return adminLoadMsg{tab: tab, content: content}

		case adminTabSetup:
			env, err := config.LoadAdminEnv(ctx.Config.Paths.DataDir)
			if err != nil {
				return adminLoadMsg{tab: tab, err: err}
			}
			if env.OpenRouterAPIKey == "" {
				env.OpenRouterAPIKey = ctx.Config.OpenRouter.APIKey
			}
			if env.DiscordBotToken == "" {
				env.DiscordBotToken = ctx.Config.Discord.BotToken
			}
			if env.SignalNumber == "" {
				env.SignalNumber = ctx.Config.Signal.AccountNumber
			}
			if env.MasterKey == "" {
				env.MasterKey = ctx.Config.Secrets.MasterKey
			}
			return adminLoadMsg{tab: tab, env: env}
		}
		return nil
	}
}

func (m adminModel) Update(msg tea.Msg) (adminModel, tea.Cmd) {
	switch msg := msg.(type) {
	case adminLoadMsg:
		if msg.err != nil {
			if msg.tab == adminTabSetup {
				m.setupStatus = "error: " + msg.err.Error()
				m.setupStatusErr = true
				return m, nil
			}
			m.setTabContent(msg.tab, styleErrorBg.Render("error: "+msg.err.Error()))
			return m, nil
		}
		switch msg.tab {
		case adminTabUsers:
			m.userList = msg.users
			m.userSel = 0
			m.rebuildUsersViewport()
		case adminTabSetup:
			m.setupOpenRouter.SetValue(msg.env.OpenRouterAPIKey)
			m.setupDiscord.SetValue(msg.env.DiscordBotToken)
			m.setupSignal.SetValue(msg.env.SignalNumber)
			m.setupMasterKey.SetValue(msg.env.MasterKey)
		default:
			m.setTabContent(msg.tab, msg.content)
		}
		return m, nil

	case adminSetupSavedMsg:
		m.setupStatus = msg.status
		m.setupStatusErr = msg.err != nil
		if msg.err != nil {
			m.setupStatus = "error: " + msg.err.Error()
		}
		return m, nil

	case tea.MouseClickMsg:
		if msg.Button == tea.MouseLeft && msg.Y == 1 {
			if tab, ok := m.hitTab(msg.X); ok {
				return m.switchTab(tab)
			}
		}

	case tea.KeyPressMsg:
		if m.tab == adminTabSetup {
			return m.updateSetupKey(msg)
		}
		switch msg.String() {
		case "1":
			return m.switchTab(adminTabAudit)
		case "2":
			return m.switchTab(adminTabUsers)
		case "3":
			return m.switchTab(adminTabJobs)
		case "4":
			return m.switchTab(adminTabSignal)
		case "5":
			return m.switchTab(adminTabSetup)
		case "[":
			next := (int(m.tab) - 1 + len(adminTabLabels)) % len(adminTabLabels)
			return m.switchTab(adminTab(next))
		case "]":
			next := (int(m.tab) + 1) % len(adminTabLabels)
			return m.switchTab(adminTab(next))
		case "r":
			return m, m.loadTabCmd(m.tab)
		case "up":
			if m.tab == adminTabUsers && m.userSel > 0 {
				m.userSel--
				m.rebuildUsersViewport()
				return m, nil
			}
		case "down":
			if m.tab == adminTabUsers && m.userSel < len(m.userList)-1 {
				m.userSel++
				m.rebuildUsersViewport()
				return m, nil
			}
		case "p":
			if m.tab == adminTabUsers && m.userSel < len(m.userList) {
				u := m.userList[m.userSel]
				_ = store.SetUserRole(m.ctx.DB, u.Username, "admin")
				return m, m.loadTabCmd(adminTabUsers)
			}
		case "d":
			if m.tab == adminTabUsers && m.userSel < len(m.userList) {
				u := m.userList[m.userSel]
				_ = store.SetUserRole(m.ctx.DB, u.Username, "user")
				return m, m.loadTabCmd(adminTabUsers)
			}
		}
	}

	var cmd tea.Cmd
	switch m.tab {
	case adminTabAudit:
		m.audit, cmd = m.audit.Update(msg)
	case adminTabUsers:
		m.users, cmd = m.users.Update(msg)
	case adminTabJobs:
		m.jobs, cmd = m.jobs.Update(msg)
	case adminTabSignal:
		m.signal, cmd = m.signal.Update(msg)
	case adminTabSetup:
		m, cmd = m.updateSetupMsg(msg)
	}
	return m, cmd
}

func (m adminModel) updateSetupKey(msg tea.KeyPressMsg) (adminModel, tea.Cmd) {
	switch msg.String() {
	case "tab", "down":
		m.setSetupFocus((m.setupFocus + 1) % 5)
		return m, nil
	case "shift+tab", "up":
		m.setSetupFocus((m.setupFocus - 1 + 5) % 5)
		return m, nil
	case "ctrl+s":
		return m, m.saveSetupCmd()
	case "enter":
		if m.setupFocus == 4 {
			return m, m.saveSetupCmd()
		}
	}

	return m.updateSetupMsg(msg)
}

func (m adminModel) updateSetupMsg(msg tea.Msg) (adminModel, tea.Cmd) {
	if m.setupFocus == 4 {
		return m, nil
	}

	var cmd tea.Cmd
	switch m.setupFocus {
	case 0:
		m.setupOpenRouter, cmd = m.setupOpenRouter.Update(msg)
	case 1:
		m.setupDiscord, cmd = m.setupDiscord.Update(msg)
	case 2:
		m.setupSignal, cmd = m.setupSignal.Update(msg)
	case 3:
		m.setupMasterKey, cmd = m.setupMasterKey.Update(msg)
	}
	return m, cmd
}

func (m adminModel) saveSetupCmd() tea.Cmd {
	ctx := m.ctx
	env := config.AdminEnv{
		OpenRouterAPIKey: m.setupOpenRouter.Value(),
		DiscordBotToken:  m.setupDiscord.Value(),
		SignalNumber:     m.setupSignal.Value(),
		MasterKey:        m.setupMasterKey.Value(),
	}
	return func() tea.Msg {
		if strings.TrimSpace(env.MasterKey) != "" {
			if _, err := secrets.NewStore(ctx.DB, env.MasterKey, time.Duration(ctx.Config.Secrets.TTLHours)*time.Hour); err != nil {
				return adminSetupSavedMsg{err: fmt.Errorf("invalid master key: %w", err)}
			}
		}
		if err := config.SaveAdminEnv(ctx.Config.Paths.DataDir, env); err != nil {
			return adminSetupSavedMsg{err: err}
		}

		ctx.Config.OpenRouter.APIKey = strings.TrimSpace(env.OpenRouterAPIKey)
		ctx.Config.Discord.BotToken = strings.TrimSpace(env.DiscordBotToken)
		ctx.Config.Signal.AccountNumber = strings.TrimSpace(env.SignalNumber)
		ctx.Config.Secrets.MasterKey = strings.TrimSpace(env.MasterKey)
		ctx.Agent.ReloadRuntimeConfig()

		return adminSetupSavedMsg{
			status: "saved to " + config.AdminEnvPath(ctx.Config.Paths.DataDir) + "  OpenRouter/master key apply now; Discord/Signal need restart",
		}
	}
}

func (m adminModel) switchTab(tab adminTab) (adminModel, tea.Cmd) {
	m.tab = tab
	return m, m.loadTabCmd(tab)
}

func (m *adminModel) setTabContent(tab adminTab, content string) {
	switch tab {
	case adminTabAudit:
		setViewportContent(&m.audit, content, colorBg)
		m.audit.GotoTop()
	case adminTabUsers:
		setViewportContent(&m.users, content, colorBg)
		m.users.GotoTop()
	case adminTabJobs:
		setViewportContent(&m.jobs, content, colorBg)
		m.jobs.GotoTop()
	case adminTabSignal:
		setViewportContent(&m.signal, content, colorBg)
		m.signal.GotoTop()
	}
}

func (m *adminModel) rebuildUsersViewport() {
	var b strings.Builder
	b.WriteString(styleTitleBg.Render("users") + "\n\n")
	for i, u := range m.userList {
		roleTag := styleDimBg.Render("[" + u.Role + "]")
		var line string
		if i == m.userSel {
			line = styleTabActive.Render(" "+u.Username+" ") + styleBodyBg.Render("  ") + roleTag
		} else {
			line = styleMutedBg.Render("  "+u.Username) + styleBodyBg.Render("  ") + roleTag
		}
		b.WriteString(line + "\n")
	}
	b.WriteString("\n" + styleDimBg.Render("↑↓ · select   p · promote   d · demote   r · refresh"))
	setViewportContent(&m.users, strings.TrimRight(b.String(), "\n"), colorBg)
}

func (m *adminModel) setSetupFocus(focus int) {
	m.setupFocus = focus
	switch focus {
	case 0:
		m.setupOpenRouter.Focus()
		m.setupDiscord.Blur()
		m.setupSignal.Blur()
		m.setupMasterKey.Blur()
	case 1:
		m.setupOpenRouter.Blur()
		m.setupDiscord.Focus()
		m.setupSignal.Blur()
		m.setupMasterKey.Blur()
	case 2:
		m.setupOpenRouter.Blur()
		m.setupDiscord.Blur()
		m.setupSignal.Focus()
		m.setupMasterKey.Blur()
	case 3:
		m.setupOpenRouter.Blur()
		m.setupDiscord.Blur()
		m.setupSignal.Blur()
		m.setupMasterKey.Focus()
	default:
		m.setupOpenRouter.Blur()
		m.setupDiscord.Blur()
		m.setupSignal.Blur()
		m.setupMasterKey.Blur()
	}
}

func (m adminModel) tabButtons() []headerButton {
	btns := make([]headerButton, len(adminTabLabels))
	curX := 0
	for i, label := range adminTabLabels {
		rendered := styleTab.Render(label)
		if adminTab(i) == m.tab {
			rendered = styleTabActive.Render(label)
		}
		w := lipgloss.Width(rendered)
		btns[i] = headerButton{ID: label, Label: label, X0: curX, X1: curX + w}
		curX += w
	}
	return btns
}

func (m adminModel) hitTab(x int) (adminTab, bool) {
	for i, b := range m.tabButtons() {
		if x >= b.X0 && x < b.X1 {
			return adminTab(i), true
		}
	}
	return adminTabAudit, false
}

func (m adminModel) renderSetup() string {
	saveLabel := styleTab.Render(" save ")
	if m.setupFocus == 4 {
		saveLabel = styleTabActive.Render(" save ")
	}

	var b strings.Builder
	b.WriteString(styleTitleBg.Render("admin setup") + "\n\n")
	b.WriteString(styleMutedBg.Render("persistent host-side env store") + "\n")
	b.WriteString(styleDimBg.Render("  "+m.setupPath) + "\n\n")
	b.WriteString(m.setupOpenRouter.View() + "\n\n")
	b.WriteString(m.setupDiscord.View() + "\n\n")
	b.WriteString(m.setupSignal.View() + "\n\n")
	b.WriteString(m.setupMasterKey.View() + "\n\n")
	b.WriteString(saveLabel + "\n\n")
	b.WriteString(styleDimBg.Render("tab/shift+tab · move   ctrl+s · save   OpenRouter/master key update live; Discord/Signal require restart"))
	if !m.ctx.Config.Discord.Enabled || !m.ctx.Config.Signal.Enabled {
		b.WriteString("\n" + styleDimBg.Render("Discord/Signal still require enabled=true in server config."))
	}
	if m.setupStatus != "" {
		line := styleInfoBg.Render(m.setupStatus)
		if m.setupStatusErr {
			line = styleErrorBg.Render(m.setupStatus)
		}
		b.WriteString("\n\n" + line)
	}
	return strings.TrimRight(b.String(), "\n")
}

func (m adminModel) View() tea.View {
	tabs := make([]string, len(adminTabLabels))
	for i, label := range adminTabLabels {
		if adminTab(i) == m.tab {
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
	switch m.tab {
	case adminTabAudit:
		body = m.audit.View()
	case adminTabUsers:
		body = m.users.View()
	case adminTabJobs:
		body = m.jobs.View()
	case adminTabSignal:
		body = m.signal.View()
	case adminTabSetup:
		setViewportContent(&m.setup, m.renderSetup(), colorBg)
		body = m.setup.View()
	}
	if m.w > 0 || m.h > 0 {
		body = fillArea(body, m.w, max(0, m.h-1), colorBg)
	}
	return tea.NewView(tabBar + "\n" + body)
}
