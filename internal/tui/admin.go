package tui

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"tether/internal/config"
	"tether/internal/llm/openrouter"
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
	adminTabAgent
)

var adminTabLabels = []string{"audit", "users", "jobs", "signal", "setup", "agent"}

type adminModel struct {
	ctx  *SessionContext
	tab  adminTab
	w, h int

	audit  viewport.Model
	users  viewport.Model
	jobs   viewport.Model
	signal viewport.Model
	setup  viewport.Model
	agent  viewport.Model

	userList []store.User
	userSel  int

	setupPath        string
	setupOpenRouter  textinput.Model
	setupModel       textinput.Model
	setupDiscord     textinput.Model
	setupSignal      textinput.Model
	setupMasterKey   textinput.Model
	setupFocus       int
	setupStatus      string
	setupStatusErr   bool
	setupModels      []openrouter.Model
	setupModelsErr   string
	setupModelSel    int
	setupEndpoints   []openrouter.ModelEndpoint
	setupEndpointID  string
	setupEndpointErr string

	agentMaxCalls  textinput.Model
	agentTimeout   textinput.Model
	agentTemp      textinput.Model
	agentEffort    textinput.Model
	agentAllowPriv textinput.Model
	agentAllowHost textinput.Model
	agentFocus     int
	agentStatus    string
	agentStatusErr bool
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

type adminAgentSavedMsg struct {
	err    error
	status string
}

type adminModelsLoadedMsg struct {
	models []openrouter.Model
	err    error
}

type adminModelEndpointsLoadedMsg struct {
	model     string
	endpoints []openrouter.ModelEndpoint
	err       error
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
		agent:           mk(),
		setupPath:       config.AdminEnvPath(ctx.Config.Paths.DataDir),
		setupOpenRouter: masked("OpenRouter API key: "),
		setupModel:      plain("OpenRouter model: "),
		setupDiscord:    masked("Discord bot token: "),
		setupSignal:     plain("Signal account number: "),
		setupMasterKey:  masked("Secrets master key: "),
		agentMaxCalls:   plain("Max tool calls/turn: "),
		agentTimeout:    plain("Turn timeout (seconds): "),
		agentTemp:       plain("Temperature: "),
		agentEffort:     plain("Reasoning effort (low|medium|high|auto): "),
		agentAllowPriv:  plain("Allow private/internal web-fetch (true|false): "),
		agentAllowHost:  plain("Allow non-sandboxed host bash (true|false): "),
	}
	m.setSetupFocus(0)
	return m
}

func (m adminModel) withSize(w, h int) adminModel {
	if w <= 0 || h <= 0 {
		return m
	}
	m.w, m.h = w, h
	ch := max(1, h-2) // 1 line for the connector strip + 1 for the tab bar
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
	m.agent.SetWidth(w)
	m.agent.SetHeight(ch)
	inputW := max(24, w-6)
	m.setupOpenRouter.SetWidth(inputW)
	m.setupModel.SetWidth(inputW)
	m.setupDiscord.SetWidth(inputW)
	m.setupSignal.SetWidth(inputW)
	m.setupMasterKey.SetWidth(inputW)
	m.agentMaxCalls.SetWidth(inputW)
	m.agentTimeout.SetWidth(inputW)
	m.agentTemp.SetWidth(inputW)
	m.agentEffort.SetWidth(inputW)
	m.agentAllowPriv.SetWidth(inputW)
	m.agentAllowHost.SetWidth(inputW)
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
			if env.OpenRouterModel == "" {
				env.OpenRouterModel = ctx.Config.OpenRouter.Model
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
			m.setupModel.SetValue(msg.env.OpenRouterModel)
			m.setupDiscord.SetValue(msg.env.DiscordBotToken)
			m.setupSignal.SetValue(msg.env.SignalNumber)
			m.setupMasterKey.SetValue(msg.env.MasterKey)
			m.syncSetupModelSelection()
			return m, tea.Batch(m.loadSetupModelsCmd(), m.loadSetupModelEndpointsCmd(msg.env.OpenRouterModel))
		default:
			m.setTabContent(msg.tab, msg.content)
		}
		return m, nil

	case adminModelsLoadedMsg:
		if msg.err != nil {
			m.setupModelsErr = msg.err.Error()
			m.setupModels = nil
			return m, nil
		}
		m.setupModelsErr = ""
		m.setupModels = append([]openrouter.Model(nil), msg.models...)
		sort.Slice(m.setupModels, func(i, j int) bool {
			return m.setupModels[i].ID < m.setupModels[j].ID
		})
		m.syncSetupModelSelection()
		if picked, ok := m.selectedSetupModel(); ok && strings.TrimSpace(m.setupModel.Value()) == "" {
			m.setupModel.SetValue(picked.ID)
		}
		return m, nil

	case adminModelEndpointsLoadedMsg:
		if strings.TrimSpace(msg.model) != strings.TrimSpace(m.setupModel.Value()) {
			return m, nil
		}
		m.setupEndpointID = strings.TrimSpace(msg.model)
		if msg.err != nil {
			m.setupEndpointErr = msg.err.Error()
			m.setupEndpoints = nil
			return m, nil
		}
		m.setupEndpointErr = ""
		m.setupEndpoints = append([]openrouter.ModelEndpoint(nil), msg.endpoints...)
		return m, nil

	case adminSetupSavedMsg:
		m.setupStatus = msg.status
		m.setupStatusErr = msg.err != nil
		if msg.err != nil {
			m.setupStatus = "error: " + msg.err.Error()
		}
		return m, nil

	case adminAgentSavedMsg:
		m.agentStatus = msg.status
		m.agentStatusErr = msg.err != nil
		if msg.err != nil {
			m.agentStatus = "error: " + msg.err.Error()
		}
		return m, nil

	case tea.MouseClickMsg:
		// Screen rows: 0 = app header, 1 = connector strip, 2 = tab bar.
		if msg.Button == tea.MouseLeft && msg.Y == 2 {
			if tab, ok := m.hitTab(msg.X); ok {
				return m.switchTab(tab)
			}
		}

	case tea.KeyPressMsg:
		if m.tab == adminTabSetup {
			return m.updateSetupKey(msg)
		}
		if m.tab == adminTabAgent {
			return m.updateAgentKey(msg)
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
		case "6":
			return m.switchTab(adminTabAgent)
		case "[", "left", "shift+tab":
			next := (int(m.tab) - 1 + len(adminTabLabels)) % len(adminTabLabels)
			return m.switchTab(adminTab(next))
		case "]", "right", "tab":
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
	case adminTabAgent:
		m, cmd = m.updateAgentMsg(msg)
	}
	return m, cmd
}

func (m adminModel) updateSetupKey(msg tea.KeyPressMsg) (adminModel, tea.Cmd) {
	switch msg.String() {
	case "tab":
		m.setSetupFocus((m.setupFocus + 1) % 7)
		return m, nil
	case "shift+tab":
		m.setSetupFocus((m.setupFocus - 1 + 7) % 7)
		return m, nil
	case "ctrl+s":
		return m, m.saveSetupCmd()
	case "enter":
		if m.setupFocus == 2 {
			return m.chooseSetupModel()
		}
		if m.setupFocus == 6 {
			return m, m.saveSetupCmd()
		}
	case "up":
		if m.setupFocus == 2 {
			m.moveSetupModelSel(-1)
			return m, nil
		}
	case "down":
		if m.setupFocus == 2 {
			m.moveSetupModelSel(1)
			return m, nil
		}
	}

	return m.updateSetupMsg(msg)
}

func (m adminModel) updateSetupMsg(msg tea.Msg) (adminModel, tea.Cmd) {
	if m.setupFocus == 2 || m.setupFocus == 6 {
		return m, nil
	}

	var cmd tea.Cmd
	var before string
	switch m.setupFocus {
	case 0:
		m.setupOpenRouter, cmd = m.setupOpenRouter.Update(msg)
	case 1:
		before = m.setupModel.Value()
		m.setupModel, cmd = m.setupModel.Update(msg)
	case 3:
		m.setupDiscord, cmd = m.setupDiscord.Update(msg)
	case 4:
		m.setupSignal, cmd = m.setupSignal.Update(msg)
	case 5:
		m.setupMasterKey, cmd = m.setupMasterKey.Update(msg)
	}
	if m.setupFocus == 1 && before != m.setupModel.Value() {
		m.syncSetupModelSelection()
		return m, tea.Batch(cmd, m.loadSetupModelEndpointsCmd(m.setupModel.Value()))
	}
	return m, cmd
}

func (m adminModel) saveSetupCmd() tea.Cmd {
	ctx := m.ctx
	env := config.AdminEnv{
		OpenRouterAPIKey: m.setupOpenRouter.Value(),
		OpenRouterModel:  m.setupModel.Value(),
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
		ctx.Config.OpenRouter.Model = strings.TrimSpace(env.OpenRouterModel)
		ctx.Config.Discord.BotToken = strings.TrimSpace(env.DiscordBotToken)
		ctx.Config.Signal.AccountNumber = strings.TrimSpace(env.SignalNumber)
		ctx.Config.Secrets.MasterKey = strings.TrimSpace(env.MasterKey)
		if ctx.Agent != nil {
			ctx.Agent.ReloadRuntimeConfig()
		}

		return adminSetupSavedMsg{
			status: "saved to " + config.AdminEnvPath(ctx.Config.Paths.DataDir) + "  OpenRouter model/key and master key apply now; Discord/Signal need restart",
		}
	}
}

// agentFocusCount is the number of focusable items on the agent tab:
// 6 inputs + the save button.
const agentFocusCount = 7

func (m adminModel) loadAgentInputs() adminModel {
	c := m.ctx.Config
	m.agentMaxCalls.SetValue(strconv.Itoa(c.AgentMaxToolCalls()))
	m.agentTimeout.SetValue(strconv.Itoa(int(c.AgentTurnTimeout().Seconds())))
	m.agentTemp.SetValue(strconv.FormatFloat(c.AgentTemperature(), 'g', -1, 64))
	m.agentEffort.SetValue(c.AgentReasoningEffort())
	m.agentAllowPriv.SetValue(strconv.FormatBool(c.Web.AllowPrivateNetwork))
	m.agentAllowHost.SetValue(strconv.FormatBool(c.HostExec.Enabled))
	m.setAgentFocus(0)
	return m
}

func (m *adminModel) setAgentFocus(focus int) {
	m.agentFocus = focus
	inputs := []*textinput.Model{&m.agentMaxCalls, &m.agentTimeout, &m.agentTemp, &m.agentEffort, &m.agentAllowPriv, &m.agentAllowHost}
	for i, in := range inputs {
		if i == focus {
			in.Focus()
		} else {
			in.Blur()
		}
	}
}

func (m adminModel) updateAgentKey(msg tea.KeyPressMsg) (adminModel, tea.Cmd) {
	switch msg.String() {
	case "tab":
		m.setAgentFocus((m.agentFocus + 1) % agentFocusCount)
		return m, nil
	case "shift+tab":
		m.setAgentFocus((m.agentFocus - 1 + agentFocusCount) % agentFocusCount)
		return m, nil
	case "ctrl+s":
		return m, m.saveAgentCmd()
	case "enter":
		if m.agentFocus == agentFocusCount-1 {
			return m, m.saveAgentCmd()
		}
	}
	return m.updateAgentMsg(msg)
}

func (m adminModel) updateAgentMsg(msg tea.Msg) (adminModel, tea.Cmd) {
	var cmd tea.Cmd
	switch m.agentFocus {
	case 0:
		m.agentMaxCalls, cmd = m.agentMaxCalls.Update(msg)
	case 1:
		m.agentTimeout, cmd = m.agentTimeout.Update(msg)
	case 2:
		m.agentTemp, cmd = m.agentTemp.Update(msg)
	case 3:
		m.agentEffort, cmd = m.agentEffort.Update(msg)
	case 4:
		m.agentAllowPriv, cmd = m.agentAllowPriv.Update(msg)
	case 5:
		m.agentAllowHost, cmd = m.agentAllowHost.Update(msg)
	}
	return m, cmd
}

func (m adminModel) saveAgentCmd() tea.Cmd {
	ctx := m.ctx
	maxCalls := strings.TrimSpace(m.agentMaxCalls.Value())
	timeout := strings.TrimSpace(m.agentTimeout.Value())
	temp := strings.TrimSpace(m.agentTemp.Value())
	effort := strings.TrimSpace(m.agentEffort.Value())
	allowPriv := strings.TrimSpace(m.agentAllowPriv.Value())
	allowHost := strings.TrimSpace(m.agentAllowHost.Value())
	return func() tea.Msg {
		// Validate before persisting so we never write garbage that the loader
		// would silently ignore.
		nCalls, err := strconv.Atoi(maxCalls)
		if err != nil || nCalls <= 0 {
			return adminAgentSavedMsg{err: fmt.Errorf("max tool calls must be a positive integer")}
		}
		nTimeout, err := strconv.Atoi(timeout)
		if err != nil || nTimeout <= 0 {
			return adminAgentSavedMsg{err: fmt.Errorf("turn timeout must be a positive integer (seconds)")}
		}
		fTemp, err := strconv.ParseFloat(temp, 64)
		if err != nil || fTemp < 0 {
			return adminAgentSavedMsg{err: fmt.Errorf("temperature must be a number >= 0")}
		}
		switch effort {
		case "low", "medium", "high", config.ReasoningEffortAuto:
		default:
			return adminAgentSavedMsg{err: fmt.Errorf("reasoning effort must be low, medium, high, or auto")}
		}
		bAllowPriv, err := strconv.ParseBool(allowPriv)
		if err != nil {
			return adminAgentSavedMsg{err: fmt.Errorf("allow private/internal web-fetch must be true or false")}
		}
		bAllowHost, err := strconv.ParseBool(allowHost)
		if err != nil {
			return adminAgentSavedMsg{err: fmt.Errorf("allow non-sandboxed host bash must be true or false")}
		}

		env, err := config.LoadAdminEnv(ctx.Config.Paths.DataDir)
		if err != nil {
			return adminAgentSavedMsg{err: err}
		}
		env.AgentMaxToolCalls = maxCalls
		env.AgentTurnTimeoutSeconds = timeout
		env.AgentTemperature = temp
		env.AgentReasoningEffort = effort
		env.WebAllowPrivateNetwork = strconv.FormatBool(bAllowPriv)
		env.HostExecEnabled = strconv.FormatBool(bAllowHost)
		if err := config.SaveAdminEnv(ctx.Config.Paths.DataDir, env); err != nil {
			return adminAgentSavedMsg{err: err}
		}

		// Apply live: the tool loop, web-fetch, and host bash read these from the
		// shared config each turn.
		ctx.Config.Agent.MaxToolCalls = nCalls
		ctx.Config.Agent.TurnTimeoutSeconds = nTimeout
		ctx.Config.Agent.Temperature = &fTemp
		ctx.Config.Agent.ReasoningEffort = effort
		ctx.Config.Web.AllowPrivateNetwork = bAllowPriv
		ctx.Config.HostExec.Enabled = bAllowHost

		return adminAgentSavedMsg{status: "saved to " + config.AdminEnvPath(ctx.Config.Paths.DataDir) + "  applies to the next turn"}
	}
}

func (m adminModel) renderAgent() string {
	saveLabel := styleTab.Render(" save ")
	if m.agentFocus == agentFocusCount-1 {
		saveLabel = styleTabActive.Render(" save ")
	}

	var b strings.Builder
	b.WriteString(styleTitleBg.Render("agent runtime") + "\n\n")
	b.WriteString(styleMutedBg.Render("per-turn tool-calling limits and sampling") + "\n")
	b.WriteString(styleDimBg.Render("  "+config.AdminEnvPath(m.ctx.Config.Paths.DataDir)) + "\n\n")
	b.WriteString(m.agentMaxCalls.View() + "\n\n")
	b.WriteString(m.agentTimeout.View() + "\n\n")
	b.WriteString(m.agentTemp.View() + "\n\n")
	b.WriteString(m.agentEffort.View() + "\n\n")
	b.WriteString(m.agentAllowPriv.View() + "\n\n")
	b.WriteString(m.agentAllowHost.View() + "\n\n")
	b.WriteString(saveLabel + "\n\n")
	b.WriteString(styleDimBg.Render("tab/shift+tab · move   ctrl+s · save"))
	b.WriteString("\n" + styleDimBg.Render(fmt.Sprintf(
		"defaults: %d calls · %ds timeout · temp %s · %s effort",
		config.DefaultAgentMaxToolCalls,
		config.DefaultAgentTurnTimeoutSeconds,
		strconv.FormatFloat(config.DefaultAgentTemperature, 'g', -1, 64),
		config.DefaultAgentReasoningEffort,
	)))
	if m.agentStatus != "" {
		line := styleInfoBg.Render(m.agentStatus)
		if m.agentStatusErr {
			line = styleErrorBg.Render(m.agentStatus)
		}
		b.WriteString("\n\n" + line)
	}
	return strings.TrimRight(b.String(), "\n")
}

func (m adminModel) switchTab(tab adminTab) (adminModel, tea.Cmd) {
	m.tab = tab
	if tab == adminTabAgent {
		m = m.loadAgentInputs()
		return m, nil
	}
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
		m.setupModel.Blur()
		m.setupDiscord.Blur()
		m.setupSignal.Blur()
		m.setupMasterKey.Blur()
	case 1:
		m.setupOpenRouter.Blur()
		m.setupModel.Focus()
		m.setupDiscord.Blur()
		m.setupSignal.Blur()
		m.setupMasterKey.Blur()
	case 3:
		m.setupOpenRouter.Blur()
		m.setupModel.Blur()
		m.setupDiscord.Focus()
		m.setupSignal.Blur()
		m.setupMasterKey.Blur()
	case 4:
		m.setupOpenRouter.Blur()
		m.setupModel.Blur()
		m.setupDiscord.Blur()
		m.setupSignal.Focus()
		m.setupMasterKey.Blur()
	case 5:
		m.setupOpenRouter.Blur()
		m.setupModel.Blur()
		m.setupDiscord.Blur()
		m.setupSignal.Blur()
		m.setupMasterKey.Focus()
	default:
		m.setupOpenRouter.Blur()
		m.setupModel.Blur()
		m.setupDiscord.Blur()
		m.setupSignal.Blur()
		m.setupMasterKey.Blur()
	}
}

func (m adminModel) loadSetupModelsCmd() tea.Cmd {
	baseURL := strings.TrimSpace(m.ctx.Config.OpenRouter.BaseURL)
	apiKey := strings.TrimSpace(m.setupOpenRouter.Value())
	if apiKey == "" {
		apiKey = strings.TrimSpace(m.ctx.Config.OpenRouter.APIKey)
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
		defer cancel()
		client := openrouter.New(baseURL, apiKey, "Tether")
		models, err := client.Models(ctx)
		return adminModelsLoadedMsg{models: models, err: err}
	}
}

func (m adminModel) loadSetupModelEndpointsCmd(modelID string) tea.Cmd {
	modelID = strings.TrimSpace(modelID)
	if modelID == "" {
		return nil
	}
	baseURL := strings.TrimSpace(m.ctx.Config.OpenRouter.BaseURL)
	apiKey := strings.TrimSpace(m.setupOpenRouter.Value())
	if apiKey == "" {
		apiKey = strings.TrimSpace(m.ctx.Config.OpenRouter.APIKey)
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
		defer cancel()
		client := openrouter.New(baseURL, apiKey, "Tether")
		endpoints, err := client.ModelEndpoints(ctx, modelID)
		return adminModelEndpointsLoadedMsg{model: modelID, endpoints: endpoints, err: err}
	}
}

func (m *adminModel) syncSetupModelSelection() {
	filtered := m.filteredSetupModels()
	if len(filtered) == 0 {
		m.setupModelSel = 0
		return
	}
	current := strings.TrimSpace(m.setupModel.Value())
	for i, model := range filtered {
		if model.ID == current {
			m.setupModelSel = i
			return
		}
	}
	if m.setupModelSel >= len(filtered) {
		m.setupModelSel = len(filtered) - 1
	}
	if m.setupModelSel < 0 {
		m.setupModelSel = 0
	}
}

func (m *adminModel) moveSetupModelSel(delta int) {
	filtered := m.filteredSetupModels()
	if len(filtered) == 0 {
		m.setupModelSel = 0
		return
	}
	m.setupModelSel += delta
	if m.setupModelSel < 0 {
		m.setupModelSel = 0
	}
	if m.setupModelSel >= len(filtered) {
		m.setupModelSel = len(filtered) - 1
	}
}

func (m adminModel) chooseSetupModel() (adminModel, tea.Cmd) {
	picked, ok := m.selectedSetupModel()
	if !ok {
		return m, nil
	}
	if m.setupModel.Value() == picked.ID {
		return m, nil
	}
	m.setupModel.SetValue(picked.ID)
	m.syncSetupModelSelection()
	return m, m.loadSetupModelEndpointsCmd(picked.ID)
}

func (m adminModel) selectedSetupModel() (openrouter.Model, bool) {
	filtered := m.filteredSetupModels()
	if len(filtered) == 0 || m.setupModelSel < 0 || m.setupModelSel >= len(filtered) {
		return openrouter.Model{}, false
	}
	return filtered[m.setupModelSel], true
}

func (m adminModel) filteredSetupModels() []openrouter.Model {
	query := strings.ToLower(strings.TrimSpace(m.setupModel.Value()))
	models := make([]openrouter.Model, 0, len(m.setupModels))
	for _, model := range m.setupModels {
		if query == "" || strings.Contains(strings.ToLower(model.ID), query) || strings.Contains(strings.ToLower(model.Name), query) {
			models = append(models, model)
		}
	}
	sort.SliceStable(models, func(i, j int) bool {
		return setupModelRank(models[i], query) < setupModelRank(models[j], query)
	})
	return models
}

func setupModelRank(model openrouter.Model, query string) string {
	id := strings.ToLower(model.ID)
	name := strings.ToLower(model.Name)
	switch {
	case query == "":
		return "3:" + id
	case id == query:
		return "0:" + id
	case strings.HasPrefix(id, query):
		return "1:" + id
	case strings.Contains(name, query):
		return "2:" + id
	default:
		return "3:" + id
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

func (m adminModel) renderSetupModelList() string {
	if m.setupModelsErr != "" {
		return styleErrorBg.Render("OpenRouter models: " + m.setupModelsErr)
	}
	filtered := m.filteredSetupModels()
	if len(filtered) == 0 {
		if len(m.setupModels) == 0 {
			return styleDimBg.Render("loading OpenRouter model catalog…")
		}
		return styleDimBg.Render("no models match current filter")
	}

	start := max(0, min(m.setupModelSel-3, len(filtered)-6))
	end := min(len(filtered), start+6)
	var b strings.Builder
	b.WriteString(styleMutedBg.Render(fmt.Sprintf("OpenRouter models (%d match)", len(filtered))) + "\n")
	for i := start; i < end; i++ {
		model := filtered[i]
		line := "  " + model.ID
		if i == m.setupModelSel {
			line = "› " + model.ID
			b.WriteString(styleTabActive.Render(line) + "\n")
			continue
		}
		if model.ID == strings.TrimSpace(m.setupModel.Value()) {
			b.WriteString(styleInfoBg.Render(line) + "\n")
			continue
		}
		b.WriteString(styleMutedBg.Render(line) + "\n")
	}
	if end < len(filtered) {
		b.WriteString(styleDimBg.Render(fmt.Sprintf("… %d more", len(filtered)-end)))
	}
	return strings.TrimRight(b.String(), "\n")
}

func (m adminModel) renderSetupModelDetails() string {
	modelID := strings.TrimSpace(m.setupModel.Value())
	if modelID == "" {
		return styleDimBg.Render("Enter or pick an OpenRouter model ID.")
	}

	model, ok := m.lookupSetupModel(modelID)
	if !ok {
		return styleDimBg.Render("Model not in loaded catalog yet; save still accepts a raw OpenRouter model ID.")
	}

	var lines []string
	lines = append(lines, styleTitleBg.Render("selected model"))
	lines = append(lines, styleAccentBg.Render(model.ID))
	if model.Name != "" && model.Name != model.ID {
		lines = append(lines, styleMutedBg.Render(model.Name))
	}
	lines = append(lines, styleMutedBg.Render(
		fmt.Sprintf(
			"context %s  out %s  tokenizer %s",
			formatTokenCount(model.ContextLength),
			formatTokenCount(model.TopProvider.MaxCompletionTokens),
			fallbackText(model.Architecture.Tokenizer, "n/a"),
		),
	))
	lines = append(lines, styleMutedBg.Render(
		fmt.Sprintf(
			"I/O %s in  %s out  cache-read %s",
			formatPricePerMillion(model.Pricing.Prompt),
			formatPricePerMillion(model.Pricing.Completion),
			formatPricePerMillion(model.Pricing.InputCacheRead),
		),
	))
	if strings.TrimSpace(model.Pricing.WebSearch) != "" {
		lines = append(lines, styleMutedBg.Render("web search "+formatFlatPrice(model.Pricing.WebSearch)+" / request"))
	}
	lines = append(lines, styleMutedBg.Render(
		fmt.Sprintf(
			"modalities %s  params %d  moderated %t",
			fallbackText(model.Architecture.Modality, "n/a"),
			len(model.SupportedParameters),
			model.TopProvider.IsModerated,
		),
	))

	if ep, ok := bestSetupEndpoint(m.setupEndpoints); ok && m.setupEndpointID == modelID {
		speed := "speed n/a"
		if ep.LatencyLast30M.Number != nil || ep.LatencyLast30M.Summary != "" || ep.ThroughputLast30M.Number != nil || ep.ThroughputLast30M.Summary != "" {
			speed = fmt.Sprintf("lat %s  thr %s", formatMetricValue(ep.LatencyLast30M), formatMetricValue(ep.ThroughputLast30M))
		}
		lines = append(lines, styleMutedBg.Render(
			fmt.Sprintf(
				"provider %s  endpoints %d  uptime30m %s  cache %t  %s",
				fallbackText(ep.ProviderName, "n/a"),
				len(m.setupEndpoints),
				formatPercent(ep.UptimeLast30M),
				ep.SupportsImplicitCaching,
				speed,
			),
		))
	} else if m.setupEndpointErr != "" && m.setupEndpointID == modelID {
		lines = append(lines, styleErrorBg.Render("provider stats: "+m.setupEndpointErr))
	} else {
		lines = append(lines, styleDimBg.Render("loading provider stats…"))
	}

	if desc := strings.TrimSpace(model.Description); desc != "" {
		lines = append(lines, "")
		lines = append(lines, styleDimBg.Render(trimRunes(desc, 220)))
	}
	return strings.Join(lines, "\n")
}

func (m adminModel) lookupSetupModel(modelID string) (openrouter.Model, bool) {
	modelID = strings.TrimSpace(modelID)
	for _, model := range m.setupModels {
		if model.ID == modelID {
			return model, true
		}
	}
	return openrouter.Model{}, false
}

func bestSetupEndpoint(endpoints []openrouter.ModelEndpoint) (openrouter.ModelEndpoint, bool) {
	if len(endpoints) == 0 {
		return openrouter.ModelEndpoint{}, false
	}
	best := endpoints[0]
	for _, ep := range endpoints[1:] {
		if ep.UptimeLast30M > best.UptimeLast30M {
			best = ep
		}
	}
	return best, true
}

func formatTokenCount(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.2fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.0fk", float64(n)/1_000)
	case n > 0:
		return strconv.Itoa(n)
	default:
		return "n/a"
	}
}

func formatPricePerMillion(raw string) string {
	v, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil || v <= 0 {
		return "n/a"
	}
	return fmt.Sprintf("$%.3f/M", v*1_000_000)
}

func formatFlatPrice(raw string) string {
	v, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil || v <= 0 {
		return "n/a"
	}
	return fmt.Sprintf("$%.4f", v)
}

func formatPercent(v float64) string {
	if v <= 0 {
		return "n/a"
	}
	return fmt.Sprintf("%.1f%%", v)
}

func formatMetricValue(v openrouter.MetricValue) string {
	if v.Number != nil {
		if v.Summary != "" {
			return fmt.Sprintf("%.1f (%s)", *v.Number, v.Summary)
		}
		return fmt.Sprintf("%.1f", *v.Number)
	}
	if strings.TrimSpace(v.Summary) == "" {
		return "n/a"
	}
	return trimRunes(v.Summary, 32)
}

func fallbackText(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}

func trimRunes(s string, maxLen int) string {
	rs := []rune(strings.TrimSpace(s))
	if len(rs) <= maxLen {
		return string(rs)
	}
	return string(rs[:maxLen]) + "…"
}

func (m adminModel) renderSetup() string {
	saveLabel := styleTab.Render(" save ")
	if m.setupFocus == 6 {
		saveLabel = styleTabActive.Render(" save ")
	}

	var b strings.Builder
	b.WriteString(styleTitleBg.Render("admin setup") + "\n\n")
	b.WriteString(styleMutedBg.Render("persistent host-side env store") + "\n")
	b.WriteString(styleDimBg.Render("  "+m.setupPath) + "\n\n")
	b.WriteString(m.setupOpenRouter.View() + "\n\n")
	b.WriteString(m.setupModel.View() + "\n\n")
	b.WriteString(m.renderSetupModelList() + "\n\n")
	b.WriteString(m.renderSetupModelDetails() + "\n\n")
	b.WriteString(m.setupDiscord.View() + "\n\n")
	b.WriteString(m.setupSignal.View() + "\n\n")
	b.WriteString(m.setupMasterKey.View() + "\n\n")
	b.WriteString(saveLabel + "\n\n")
	b.WriteString(styleDimBg.Render("tab/shift+tab · move   ↑↓ · model list   enter · choose model   ctrl+s · save"))
	b.WriteString("\n" + styleDimBg.Render("OpenRouter model/key and master key update live; Discord/Signal require restart"))
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
	case adminTabAgent:
		setViewportContent(&m.agent, m.renderAgent(), colorBg)
		body = m.agent.View()
	}
	if m.w > 0 || m.h > 0 {
		body = fillArea(body, m.w, max(0, m.h-2), colorBg)
	}
	return tea.NewView(m.renderConnectorStrip() + "\n" + tabBar + "\n" + body)
}

// renderConnectorStrip renders the Console's shared connector-health line.
func (m adminModel) renderConnectorStrip() string {
	model := ""
	if m.ctx != nil && m.ctx.Agent != nil {
		model = m.ctx.Agent.Model()
	}
	var h connectorHealth
	if m.ctx != nil {
		h = connectorHealthFor(m.ctx.DB, m.ctx.Config, model, m.ctx.ConnectorsLive)
	}
	bg := lipgloss.NewStyle().Background(lipgloss.Color("232"))
	dot := func(st connState) string {
		c := colorDim
		switch st {
		case connOnline:
			c = colorGreen
		case connWarn:
			c = colorWarn
		}
		return bg.Foreground(c).Render(glyphOnline)
	}
	seg := func(st connState, label string) string {
		return dot(st) + styleStatusVal.Render(" ") + styleStatusDim.Render(label)
	}
	gap3 := bg.Render("   ")
	line := bg.Render("  ") + seg(h.signal, "signal") + gap3 + seg(h.discord, "discord") + gap3 + seg(h.jobs, "proactive")
	if h.model != "" {
		line += gap3 + styleStatusDim.Render("· "+h.model)
	}
	if m.w > 0 {
		if gapW := m.w - lipgloss.Width(line); gapW > 0 {
			line += bg.Render(strings.Repeat(" ", gapW))
		}
	}
	return line
}
