package tui

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"charm.land/bubbles/v2/cursor"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"tether/internal/agent"
	"tether/internal/config"
	"tether/internal/personality"
	"tether/internal/proactive"
	"tether/internal/redact"
	"tether/internal/store"
	"tether/internal/subagents"
	"tether/internal/tools"
	"tether/internal/userspace"
)

type viewMode int

const (
	viewLogin viewMode = iota
	viewSignup
	viewChat
	viewMemory
	viewSettings
	viewAdmin
)

type headerButton struct {
	ID    string
	Label string
	X0    int // inclusive
	X1    int // exclusive
}

type appModel struct {
	ctx *SessionContext

	w int
	h int

	view viewMode

	auth     authModel
	chat     chatModel
	memory   memoryModel
	settings settingsModel
	admin    adminModel

	user *store.User
	conv *store.Conversation

	ag      *agent.Agent
	toolReg *tools.Registry
	subMgr  *subagents.Manager
	proEng  *proactive.Engine

	nextRequestID int
	activeRuns    int
	waitlist      []string
	releasedRuns  map[int]bool

	connHealth connectorHealth
}

// connState is a connector's glanceable health for the header cluster.
type connState int

const (
	connOff connState = iota
	connWarn
	connOnline
)

// connectorHealth is a cheap snapshot of connector + job state shown in the
// header. Refreshed on the periodic poll, never computed per keystroke.
type connectorHealth struct {
	signal  connState
	discord connState
	jobs    connState
	model   string
}

type agentAsyncMsg struct {
	ch   <-chan tea.Msg
	msg  tea.Msg
	done bool
}

const tuiAgentIdleTimeout = 45 * time.Second

type agentStreamDeltaMsg struct {
	ConversationID int64
	RequestID      int
	Text           string
}

type agentStreamReasoningMsg struct {
	ConversationID int64
	RequestID      int
	Text           string
}

type agentStreamToolMsg struct {
	ConversationID int64
	RequestID      int
	Tool           toolCallEntry
}

type agentReleaseMsg struct {
	ConversationID int64
	RequestID      int
}

// autoLoginMsg triggers terminal-mode entry as the local root account,
// bypassing the login/signup view.
type autoLoginMsg struct{}

type appBackendSyncPollMsg struct{}

type appBackendSyncMsg struct {
	Conversation *store.Conversation
	Messages     []chatMessage
}

func usageBlock(lines ...string) string {
	if len(lines) == 0 {
		return "usage:"
	}
	return "usage:\n  " + strings.Join(lines, "\n  ")
}

func NewAppModel(ctx *SessionContext) tea.Model {
	ag := ctx.Agent
	m := appModel{
		ctx:           ctx,
		ag:            ag,
		toolReg:       ag.ToolRegistry(),
		subMgr:        ag.Subagents(),
		nextRequestID: 1,
		releasedRuns:  map[int]bool{},
	}
	m.proEng = proactive.NewEngine(ctx.DB, ag, ag, ag.Subagents(), ctx.Config.Paths.DataDir)
	m.view = viewLogin
	m.auth = newAuthModel(authModeLogin).withDisclaimer(ctx.Term.Disclaimer)
	m.chat = newChatModel().withTerminalProfile(ctx.Term)
	m.memory = newMemoryModel()
	m.settings = newSettingsModel(ctx)
	m.admin = newAdminModel(ctx)
	return m
}

func (m appModel) termProfile() TerminalProfile {
	if m.ctx == nil {
		return TerminalProfile{}
	}
	return m.ctx.Term
}

func (m appModel) activateConversation(conv *store.Conversation) appModel {
	m.conv = conv
	m.view = viewChat
	term := m.termProfile()
	m.chat = newChatModel().
		withTerminalProfile(term).
		withComposerContext(m.ctx.Config.Paths.DataDir, m.user != nil && m.user.Role == "admin").
		withConversation(m.ctx.DB, m.user.ID, m.conv.ID)
	m.chat = m.chat.withSize(m.w, m.h-1)
	return m
}

// provisionAndEnter sets up per-user state for an authenticated user and
// transitions into the chat view. Shared by interactive login and the
// terminal-mode root auto-login.
func (m appModel) provisionAndEnter(u *store.User) (appModel, tea.Cmd, error) {
	conv, err := store.GetOrCreateActiveConversation(m.ctx.DB, u.ID)
	if err != nil {
		return m, nil, err
	}

	// Deliver any pending proactive notifications.
	// If a notification has a conversation_id, inject it into that conversation.
	// Otherwise, inject into the user's active conversation (backwards-compatible behavior).
	nots, err := store.ListUndeliveredNotifications(m.ctx.DB, u.ID, 50)
	if err == nil {
		for _, n := range nots {
			targetConvID := conv.ID
			if n.ConversationID != 0 {
				targetConvID = n.ConversationID
			}
			note := "[Proactive/" + n.Kind + "] " + n.Content
			_ = store.AddMessage(m.ctx.DB, targetConvID, "system", note)
			_ = store.MarkNotificationDelivered(m.ctx.DB, n.ID)
		}
	}

	// Ensure per-user sandbox/config directories exist.
	dirs := userspace.ForUser(m.ctx.Config.Paths.DataDir, u.ID)
	if err := userspace.Ensure(dirs); err != nil {
		return m, nil, err
	}
	// Ensure default proactive rules exist in SQLite (spec requirement).
	b, _ := yaml.Marshal(proactive.DefaultRules())
	_ = store.EnsureDefaultProactiveRulesYAML(m.ctx.DB, u.ID, string(b))

	// Also write legacy per-user proactive rules file for transparency/editing.
	rulesPath := proactive.DefaultRulesPath(dirs.Config)
	if _, err := os.Stat(rulesPath); err != nil {
		_ = os.WriteFile(rulesPath, b, 0o644)
	}

	// Ensure default agent personalities exist (self-editable by the agent).
	_ = userspace.EnsurePersonalityFile(dirs, personality.AgentChat)
	_ = userspace.EnsurePersonalityFile(dirs, personality.AgentProactiveDailyBrief)
	_ = userspace.EnsurePersonalityFile(dirs, personality.AgentProactiveOpenLoops)
	if rules, err := proactive.LoadRules(rulesPath); err == nil {
		for _, ar := range rules.Agents {
			if id, ok := personality.NormalizeID(ar.ID); ok {
				_ = userspace.EnsurePersonalityFile(dirs, personality.ProactiveAgentKey(id))
			}
		}
	}

	m.user = u
	m = m.activateConversation(conv)
	m.memory = m.memory.withUser(m.ctx.DB, m.user.ID).withSize(m.w, m.h-1)
	m.settings = m.settings.withUser(m.user.ID).withSize(m.w, m.h-1)
	m = m.refreshChatMetrics()
	return m, tea.Batch(
		m.chat.loadCmd(),
		m.triggerProactiveEventCmd(proactive.EventLogin, nil),
		m.backendSyncTickCmd(),
	), nil
}

func (m appModel) Init() tea.Cmd {
	cmds := []tea.Cmd{
		tea.RequestBackgroundColor,
		cursor.Blink,
	}
	if m.ctx != nil && m.ctx.AutoLogin {
		cmds = append(cmds, func() tea.Msg { return autoLoginMsg{} })
	}
	return tea.Batch(cmds...)
}

func (m appModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.chat = m.chat.withSize(m.w, m.h-1) // header consumes 1 line
		m.memory = m.memory.withSize(m.w, m.h-1)
		m.auth = m.auth.withSize(m.w, m.h-1)
		m.settings = m.settings.withSize(m.w, m.h-1)
		m.admin = m.admin.withSize(m.w, m.h-1)
		return m, nil

	case tea.KeyPressMsg:
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		}

	case tea.MouseClickMsg:
		if msg.Button == tea.MouseLeft && msg.Y == 0 {
			if b, ok := m.hitHeader(msg.X); ok {
				switch b.ID {
				case "login":
					m.view = viewLogin
					m.auth = newAuthModel(authModeLogin).withDisclaimer(m.termProfile().Disclaimer).withSize(m.w, m.h-1)
				case "signup":
					m.view = viewSignup
					m.auth = newAuthModel(authModeSignup).withDisclaimer(m.termProfile().Disclaimer).withSize(m.w, m.h-1)
				case "chat":
					m.view = viewChat
				case "memory":
					m.view = viewMemory
					return m, m.memory.loadCmd()
				case "settings":
					m.view = viewSettings
					m.settings = m.settings.withSize(m.w, m.h-1)
					return m, m.settings.loadCmd()
				case "admin":
					m.view = viewAdmin
					m.admin = m.admin.withSize(m.w, m.h-1)
					return m, m.admin.loadCmd()
				}
				return m, nil
			}
		}

	case authSwitchModeMsg:
		if msg.Mode == authModeLogin {
			m.view = viewLogin
		} else {
			m.view = viewSignup
		}
		m.auth = newAuthModel(msg.Mode).withDisclaimer(m.termProfile().Disclaimer).withSize(m.w, m.h-1)
		return m, nil

	case authSubmitMsg:
		// In-app authentication (separate from SSH portal auth).
		var u *store.User
		var err error
		if msg.Mode == authModeSignup {
			u, err = store.CreateUser(m.ctx.DB, msg.Username, msg.Password)
		} else {
			u, err = store.Authenticate(m.ctx.DB, msg.Username, msg.Password)
		}
		if err != nil {
			m.auth, _ = m.auth.Update(authStatusMsg{Text: err.Error(), IsErr: true})
			return m, nil
		}
		var cmd tea.Cmd
		m, cmd, err = m.provisionAndEnter(u)
		if err != nil {
			m.auth, _ = m.auth.Update(authStatusMsg{Text: err.Error(), IsErr: true})
			return m, nil
		}
		return m, cmd

	case autoLoginMsg:
		// Terminal mode: enter as the local root account, no login screen.
		u, err := store.EnsureRootUser(m.ctx.DB)
		if err == nil {
			var cmd tea.Cmd
			m, cmd, err = m.provisionAndEnter(u)
			if err == nil {
				return m, cmd
			}
		}
		// Fall back to the login view so the error is visible.
		m.auth, _ = m.auth.Update(authStatusMsg{Text: err.Error(), IsErr: true})
		return m, nil

	case loginSuccessMsg:
		m.user = msg.User
		m = m.activateConversation(msg.Conv)
		m.memory = m.memory.withUser(m.ctx.DB, m.user.ID).withSize(m.w, m.h-1)
		m.settings = m.settings.withUser(m.user.ID).withSize(m.w, m.h-1)
		m = m.refreshChatMetrics()
		return m, tea.Batch(
			m.chat.loadCmd(),
			m.triggerProactiveEventCmd(proactive.EventLogin, nil),
			m.backendSyncTickCmd(),
		)

	case appBackendSyncPollMsg:
		m = m.refreshChatMetrics()
		return m, tea.Batch(m.backendSyncNowCmd(), m.backendSyncTickCmd())

	case appBackendSyncMsg:
		if msg.Conversation == nil {
			return m, nil
		}
		if m.conv == nil || m.conv.ID != msg.Conversation.ID {
			m = m.activateConversation(msg.Conversation)
		}
		m.chat, _ = m.chat.Update(chatLoadedMsg{Messages: msg.Messages})
		return m, nil

	case chatSendMsg:
		if strings.HasPrefix(msg.Text, "/") {
			m2, handled, cmd := m.handleCommand(msg.Text)
			if handled {
				return m2, cmd
			}
		}
		if strings.HasPrefix(msg.Text, "$") {
			m2, handled, cmd := m.handleSkillCommand(msg.Text)
			if handled {
				return m2, cmd
			}
		}
		if m.user != nil && m.conv != nil && m.ag.HasPendingConfirmation(m.user.ID, m.conv.ID) {
			note := "Pending tool confirmation rejected by the user."
			_ = m.ag.RejectPendingConfirmation(m.user.ID, m.conv.ID)
			_ = store.AddMessage(m.ctx.DB, m.conv.ID, "system", note)
			m.chat = m.chat.appendLocal("System", note)
		}

		// Redaction monitor (regex/heuristics) for user messages.
		clean, findings := redact.ScanAndRedact(msg.Text)
		if len(findings) > 0 {
			warn := "Sensitive data detected and redacted. Don’t share passwords or access tokens in chat. Use /secret add <label> <secret> instead."
			_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", warn)
			m.chat = m.chat.appendLocal("System", warn)
		}

		// Store user message then ask agent in the background.
		_ = store.AddMessage(m.ctx.DB, m.conv.ID, "user", clean)
		m.chat = m.chat.appendLocal("You", clean)
		m.waitlist = append(m.waitlist, clean)
		runCmd := m.maybeDispatchWaitlist()
		m = m.refreshChatMetrics()
		return m, tea.Batch(
			runCmd,
			m.triggerProactiveEventCmd(proactive.EventUserMessage, map[string]string{"text": clean}),
		)

	case agentAsyncMsg:
		if msg.done {
			return m, nil
		}
		var cmd tea.Cmd
		switch inner := msg.msg.(type) {
		case agentStreamDeltaMsg:
			if m.conv != nil && inner.ConversationID == m.conv.ID {
				m.chat = m.chat.setStreamingAssistant(inner.RequestID, inner.Text)
			}
		case agentStreamReasoningMsg:
			if m.conv != nil && inner.ConversationID == m.conv.ID {
				m.chat = m.chat.setStreamingReasoning(inner.RequestID, inner.Text)
			}
		case agentStreamToolMsg:
			if m.conv != nil && inner.ConversationID == m.conv.ID {
				m.chat = m.chat.upsertStreamingToolCall(inner.RequestID, inner.Tool)
			}
		case agentReleaseMsg:
			cmd = m.releaseWaitlistFor(inner.RequestID)
		case agentReplyMsg:
			m, cmd = m.handleAgentReply(inner)
		}
		return m, tea.Batch(cmd, waitAgentAsyncCmd(msg.ch))

	case agentReplyMsg:
		return m.handleAgentReply(msg)

	case cursor.BlinkMsg:
		// pass through to submodels that care (textarea).
	}

	// Delegate to current view.
	var cmd tea.Cmd
	switch m.view {
	case viewLogin, viewSignup:
		m.auth, cmd = m.auth.Update(msg)
	case viewChat:
		m.chat, cmd = m.chat.Update(msg)
	case viewMemory:
		m.memory, cmd = m.memory.Update(msg)
	case viewSettings:
		m.settings, cmd = m.settings.Update(msg)
	case viewAdmin:
		m.admin, cmd = m.admin.Update(msg)
	default:
		// no-op
	}
	return m, cmd
}

func (m appModel) View() tea.View {
	header := m.renderHeader()

	var body tea.View
	switch m.view {
	case viewLogin, viewSignup:
		body = m.auth.View()
	case viewChat:
		body = m.chat.View()
	case viewMemory:
		body = m.memory.View()
	case viewSettings:
		body = m.settings.View()
	case viewAdmin:
		body = m.admin.View()
	default:
		body = tea.NewView(styleDim.Render("unknown view"))
	}

	v := tea.NewView(header + "\n" + body.Content)
	v.AltScreen = true
	v.MouseMode = tea.MouseModeAllMotion

	// Maintain cursor from the focused sub-view.
	// Body starts at row 1 (after the header), so add 1 to all cursor Y values.
	if m.view == viewChat {
		c := m.chat.cursor()
		if c != nil {
			c.Y++ // offset for the header row
		}
		v.Cursor = c
	}
	if m.view == viewLogin || m.view == viewSignup {
		c := m.auth.cursor()
		if c != nil {
			c.Y++ // offset for the header row
		}
		v.Cursor = c
	}
	return v
}

func (m appModel) renderHeader() string {
	if m.w <= 0 {
		return ""
	}

	brand, _, tabs, userBadge := m.headerLayout()
	return styleHeaderBar.Width(m.w).Render(brand + tabs + userBadge)
}

func (m appModel) headerLayout() (brand string, buttons []headerButton, tabs string, userBadge string) {
	term := m.termProfile()
	brand = styleHeaderBrand.Render(renderBrandWordmark("TETHER", colorHeaderBg, !term.DisableGradients))

	buttons = m.headerButtons()
	tabParts := make([]string, 0, len(buttons))
	renderedWidths := make([]int, len(buttons))
	for i, b := range buttons {
		active := false
		switch b.ID {
		case "login":
			active = m.view == viewLogin
		case "signup":
			active = m.view == viewSignup
		case "chat":
			active = m.view == viewChat
		case "memory":
			active = m.view == viewMemory
		case "settings":
			active = m.view == viewSettings
		case "admin":
			active = m.view == viewAdmin
		}
		var rendered string
		if active {
			rendered = styleTabActive.Render("▸ " + b.Label)
		} else {
			rendered = styleTab.Render(b.Label)
		}
		renderedWidths[i] = lipgloss.Width(rendered)
		tabParts = append(tabParts, rendered)
	}
	tabs = lipgloss.JoinHorizontal(lipgloss.Top, tabParts...)

	// Right side: connector cluster + model badge + terminal notice + account.
	if m.user != nil {
		userBadge = renderConnectorCluster(m.connHealth)
	}
	if label := strings.TrimSpace(term.HeaderLabel); label != "" {
		notice := styleHeaderNotice.Render(label)
		if userBadge != "" {
			userBadge = lipgloss.JoinHorizontal(lipgloss.Top, userBadge, notice)
		} else {
			userBadge = notice
		}
	}

	if m.user != nil {
		accountBadge := lipgloss.JoinHorizontal(
			lipgloss.Top,
			styleHeaderUserDot.Render("●"),
			styleHeaderUserText.Render(m.user.Username),
		)
		if userBadge != "" {
			userBadge = lipgloss.JoinHorizontal(lipgloss.Top, userBadge, accountBadge)
		} else {
			userBadge = accountBadge
		}
	}

	if lipgloss.Width(brand)+lipgloss.Width(userBadge) > m.w {
		userBadge = ""
	}

	maxTabsW := max(0, m.w-lipgloss.Width(brand)-lipgloss.Width(userBadge))
	if lipgloss.Width(tabs) > maxTabsW {
		tabs = lipgloss.NewStyle().
			Background(colorHeaderBg).
			Width(maxTabsW).
			MaxWidth(maxTabsW).
			Render(tabs)
	}

	usedW := lipgloss.Width(brand) + lipgloss.Width(tabs) + lipgloss.Width(userBadge)
	gap := max(0, m.w-usedW)
	spacer := styleHeaderSpacer.Width(gap).Render(" ")
	curX := lipgloss.Width(brand) + gap
	for i := range buttons {
		buttons[i].X0 = curX
		buttons[i].X1 = curX + renderedWidths[i]
		curX = buttons[i].X1
	}

	tabs = spacer + tabs
	return brand, buttons, tabs, userBadge
}

// renderConnectorCluster renders the header's "●sig ●dsc ●job  model" block.
func renderConnectorCluster(h connectorHealth) string {
	dot := func(st connState) lipgloss.Style {
		switch st {
		case connOnline:
			return styleConnOnline
		case connWarn:
			return styleConnWarn
		default:
			return styleConnOff
		}
	}
	one := func(st connState, label string) string {
		return dot(st).Render(glyphOnline) + styleConnLabel.Render(label+" ")
	}
	cluster := styleConnLabel.Render(" ") +
		one(h.signal, "sig") + one(h.discord, "dsc") + one(h.jobs, "job")
	if m := strings.TrimSpace(h.model); m != "" {
		cluster = lipgloss.JoinHorizontal(lipgloss.Top, cluster, styleHeaderModel.Render(m))
	}
	return cluster
}

func (m appModel) headerButtons() []headerButton {
	// Keep this in sync with renderHeader.
	if m.user == nil {
		return nil // Auth screen has its own mode-switcher; no need to duplicate in header.
	}
	btns := []headerButton{
		{ID: "chat", Label: "Chat"},
		{ID: "memory", Label: "Memory"},
		{ID: "settings", Label: "Settings"},
	}
	if m.user.Role == "admin" {
		btns = append(btns, headerButton{ID: "admin", Label: "Console"})
	}
	return btns
}

func (m appModel) hitHeader(x int) (headerButton, bool) {
	_, btns, _, _ := m.headerLayout()
	for _, b := range btns {
		if x >= b.X0 && x < b.X1 {
			return b, true
		}
	}
	return headerButton{}, false
}

func (m appModel) handleSkillCommand(text string) (appModel, bool, tea.Cmd) {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return m, true, nil
	}
	if m.conv == nil || m.user == nil {
		return m, true, nil
	}
	skillName := strings.TrimPrefix(fields[0], "$")
	if strings.TrimSpace(skillName) == "" {
		resp := "usage: $<skill-name> [args]"
		_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
		m.chat = m.chat.appendLocal("System", resp)
		return m, true, nil
	}
	args := strings.TrimSpace(strings.TrimPrefix(text, fields[0]))
	_ = store.AddMessage(m.ctx.DB, m.conv.ID, "user", text)
	m.chat = m.chat.appendLocal("You", text)
	return m, true, m.invokeSkillAndAskAgentCmd(skillName, args)
}

func (m appModel) askAgentCmd(text string) tea.Cmd {
	return m.askAgentCmdWithID(m.nextRequestID, text)
}

func newTUIAgentStreamContext() (context.Context, context.CancelFunc, func()) {
	return newTUIAgentStreamContextWithIdleTimeout(tuiAgentIdleTimeout)
}

func newTUIAgentStreamContextWithIdleTimeout(idleTimeout time.Duration) (context.Context, context.CancelFunc, func()) {
	ctx, cancel := context.WithCancel(context.Background())
	activity := make(chan struct{}, 1)
	done := make(chan struct{})
	var once sync.Once

	go func() {
		timer := time.NewTimer(idleTimeout)
		defer timer.Stop()
		for {
			select {
			case <-timer.C:
				cancel()
				return
			case <-activity:
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				timer.Reset(idleTimeout)
			case <-done:
				return
			case <-ctx.Done():
				return
			}
		}
	}()

	stop := func() {
		once.Do(func() {
			close(done)
			cancel()
		})
	}
	touch := func() {
		select {
		case activity <- struct{}{}:
		default:
		}
	}
	return ctx, stop, touch
}

func (m appModel) askAgentCmdWithID(requestID int, text string) tea.Cmd {
	userID := m.user.ID
	convID := m.conv.ID
	ag := m.ag
	ch := make(chan tea.Msg, 64)
	go func() {
		defer close(ch)
		ctx, stop, touch := newTUIAgentStreamContext()
		defer stop()

		released := false
		var reasoning strings.Builder
		reply, err := ag.ReplyStream(ctx, agent.ReplyParams{UserID: userID, ConversationID: convID, Text: text}, func(ev agent.StreamEvent) {
			touch()
			switch ev.Type {
			case "assistant_delta":
				ch <- agentStreamDeltaMsg{ConversationID: convID, RequestID: requestID, Text: ev.Text}
			case "reasoning_delta":
				reasoning.WriteString(ev.Delta)
				ch <- agentStreamReasoningMsg{ConversationID: convID, RequestID: requestID, Text: reasoning.String()}
			case "tool_call":
				ch <- agentStreamToolMsg{ConversationID: convID, RequestID: requestID, Tool: toolCallEntry{Name: ev.Tool.Name, Args: ev.Tool.Args}}
			case "tool_result":
				ch <- agentStreamToolMsg{ConversationID: convID, RequestID: requestID, Tool: toolCallEntry{Name: ev.Tool.Name, Args: ev.Tool.Args, Result: ev.Tool.Result}}
				if !released {
					released = true
					ch <- agentReleaseMsg{ConversationID: convID, RequestID: requestID}
				}
			}
		})
		if err != nil {
			if !released {
				ch <- agentReleaseMsg{ConversationID: convID, RequestID: requestID}
			}
			ch <- agentReplyMsg{ConversationID: convID, RequestID: requestID, Text: "(agent error) " + err.Error()}
			return
		}
		if !released {
			ch <- agentReleaseMsg{ConversationID: convID, RequestID: requestID}
		}
		entries := make([]toolCallEntry, len(reply.ToolCalls))
		for i, tc := range reply.ToolCalls {
			entries[i] = toolCallEntry{Name: tc.Name, Args: tc.Args, Result: tc.Result}
		}
		ch <- agentReplyMsg{ConversationID: convID, RequestID: requestID, Text: reply.Text, Reasoning: reply.Reasoning, ToolCalls: entries, ReasoningItems: reply.ReasoningItems, Model: ag.Model()}
	}()
	return waitAgentAsyncCmd(ch)
}

func (m appModel) resumeConfirmationCmd(requestID int, token string) tea.Cmd {
	userID := m.user.ID
	ag := m.ag
	ch := make(chan tea.Msg, 64)
	go func() {
		defer close(ch)
		ctx, stop, touch := newTUIAgentStreamContext()
		defer stop()

		released := false
		convID := m.conv.ID
		var reasoning strings.Builder

		// Default behavior: if the token is tied to a suspended tool execution, resume it.
		// Otherwise treat /confirm as a standalone confirmation (e.g. tokens created via confirm.request).
		if !ag.HasPendingConfirmationToken(userID, token) {
			ok := ag.ConfirmToken(userID, token)
			if !released {
				released = true
				ch <- agentReleaseMsg{ConversationID: convID, RequestID: requestID}
			}
			if ok {
				ch <- agentReplyMsg{ConversationID: convID, RequestID: requestID, Text: "confirmed"}
			} else {
				ch <- agentReplyMsg{ConversationID: convID, RequestID: requestID, Text: "confirmation failed"}
			}
			return
		}

		reply, convID, ok, err := ag.ResumeConfirmedStream(ctx, userID, token, func(ev agent.StreamEvent) {
			touch()
			switch ev.Type {
			case "assistant_delta":
				ch <- agentStreamDeltaMsg{ConversationID: convID, RequestID: requestID, Text: ev.Text}
			case "reasoning_delta":
				reasoning.WriteString(ev.Delta)
				ch <- agentStreamReasoningMsg{ConversationID: convID, RequestID: requestID, Text: reasoning.String()}
			case "tool_call":
				ch <- agentStreamToolMsg{ConversationID: convID, RequestID: requestID, Tool: toolCallEntry{Name: ev.Tool.Name, Args: ev.Tool.Args}}
			case "tool_result":
				ch <- agentStreamToolMsg{ConversationID: convID, RequestID: requestID, Tool: toolCallEntry{Name: ev.Tool.Name, Args: ev.Tool.Args, Result: ev.Tool.Result}}
				if !released {
					released = true
					ch <- agentReleaseMsg{ConversationID: convID, RequestID: requestID}
				}
			}
		})
		if err != nil {
			if !released {
				ch <- agentReleaseMsg{ConversationID: convID, RequestID: requestID}
			}
			ch <- agentReplyMsg{ConversationID: convID, RequestID: requestID, Text: "(agent error) " + err.Error()}
			return
		}
		if !ok {
			if !released {
				ch <- agentReleaseMsg{ConversationID: m.conv.ID, RequestID: requestID}
			}
			ch <- agentReplyMsg{ConversationID: m.conv.ID, RequestID: requestID, Text: "confirmation failed"}
			return
		}
		if !released {
			ch <- agentReleaseMsg{ConversationID: convID, RequestID: requestID}
		}
		entries := make([]toolCallEntry, len(reply.ToolCalls))
		for i, tc := range reply.ToolCalls {
			entries[i] = toolCallEntry{Name: tc.Name, Args: tc.Args, Result: tc.Result}
		}
		ch <- agentReplyMsg{ConversationID: convID, RequestID: requestID, Text: reply.Text, Reasoning: reply.Reasoning, ToolCalls: entries, ReasoningItems: reply.ReasoningItems, Model: ag.Model()}
	}()
	return waitAgentAsyncCmd(ch)
}

func (m appModel) invokeSkillAndAskAgentCmd(skillName string, args string) tea.Cmd {
	userID := m.user.ID
	convID := m.conv.ID
	ag := m.ag
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		if _, err := ag.InvokeSkill(ctx, userID, convID, skillName, args, "", "user"); err != nil {
			return agentReplyMsg{ConversationID: convID, Text: "(skill error) " + err.Error()}
		}
		reply, err := ag.Reply(ctx, agent.ReplyParams{UserID: userID, ConversationID: convID, Text: ""})
		if err != nil {
			return agentReplyMsg{ConversationID: convID, Text: "(agent error) " + err.Error()}
		}
		entries := make([]toolCallEntry, len(reply.ToolCalls))
		for i, tc := range reply.ToolCalls {
			entries[i] = toolCallEntry{Name: tc.Name, Args: tc.Args, Result: tc.Result}
		}
		return agentReplyMsg{ConversationID: convID, Text: reply.Text, Reasoning: reply.Reasoning, ToolCalls: entries, ReasoningItems: reply.ReasoningItems, Model: ag.Model()}
	}
}

func waitAgentAsyncCmd(ch <-chan tea.Msg) tea.Cmd {
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		msg, ok := <-ch
		if !ok {
			return agentAsyncMsg{ch: ch, done: true}
		}
		return agentAsyncMsg{ch: ch, msg: msg}
	}
}

func renderSessionStatusTUI(st agent.SessionStatus) string {
	var b strings.Builder
	b.WriteString("```text\n")
	b.WriteString("Session status\n")
	b.WriteString("session_id: ")
	b.WriteString(emptyDash(st.SessionID))
	b.WriteString("\nconversation_id: ")
	b.WriteString(strconv.FormatInt(st.ConversationID, 10))
	b.WriteString("\nuser_id: ")
	b.WriteString(strconv.FormatInt(st.UserID, 10))
	b.WriteString("\nstarted: ")
	b.WriteString(formatStatusTimeLocal(st.StartedAt))
	b.WriteString("\nlast_activity: ")
	b.WriteString(formatStatusTimeLocal(st.LastActivityAt))
	b.WriteString("\nage: ")
	b.WriteString(formatStatusDuration(st.Age))
	b.WriteString("\nidle: ")
	b.WriteString(formatStatusDuration(st.Idle))
	b.WriteString("\nruntime_session: ")
	if st.HasRuntimeSession {
		b.WriteString("active")
	} else {
		b.WriteString("none")
	}
	b.WriteString("\ntool_calls: ")
	if st.HasRuntimeSession {
		b.WriteString(strconv.Itoa(st.TotalToolCalls))
	} else {
		b.WriteString("unknown")
	}
	b.WriteString("\nusage_source: ")
	b.WriteString(st.UsageSource)
	if !st.LastUsageAt.IsZero() {
		b.WriteString("\nlast_usage_at: ")
		b.WriteString(formatStatusTimeLocal(st.LastUsageAt))
	}
	b.WriteString("\ncost_usd: ")
	if st.UsageSource == "none" {
		b.WriteString("unknown")
	} else {
		b.WriteString(fmt.Sprintf("%.6f", st.TotalCost))
	}
	b.WriteString("\nmodel: ")
	if st.UsageSource == "none" {
		b.WriteString("unknown")
	} else {
		b.WriteString(emptyDash(st.LastModel))
	}
	b.WriteString("\nlast_request_context: ")
	if st.LastContextLimit > 0 {
		b.WriteString(fmt.Sprintf("%d / %d (%.1f%%)", st.LastInputTokens, st.LastContextLimit, st.LastContextPct))
	} else {
		if st.UsageSource == "none" {
			b.WriteString("unknown")
		} else {
			b.WriteString("context limit unknown")
		}
	}
	b.WriteString("\n\nAttached context\n")
	if st.AttachedContext.Available {
		b.WriteString("personality: ")
		b.WriteString(formatYesNo(st.AttachedContext.PersonalityAttached))
		b.WriteString("\nsummary: ")
		if st.AttachedContext.SummaryAttached {
			b.WriteString("yes")
			if st.AttachedContext.SummaryThroughID > 0 {
				b.WriteString(" (through message ")
				b.WriteString(strconv.FormatInt(st.AttachedContext.SummaryThroughID, 10))
				b.WriteString(")")
			}
		} else {
			b.WriteString("no")
		}
		b.WriteString("\nhistory_messages: ")
		b.WriteString(strconv.Itoa(st.AttachedContext.HistoryMessages))
		b.WriteString(" (user ")
		b.WriteString(strconv.Itoa(st.AttachedContext.HistoryUserMessages))
		b.WriteString(", assistant ")
		b.WriteString(strconv.Itoa(st.AttachedContext.HistoryAssistMessages))
		b.WriteString(")")
		b.WriteString("\nmemory: facts ")
		b.WriteString(strconv.Itoa(st.AttachedContext.MemoryFacts))
		b.WriteString(", prefs ")
		b.WriteString(strconv.Itoa(st.AttachedContext.MemoryPrefs))
		b.WriteString(", tasks ")
		b.WriteString(strconv.Itoa(st.AttachedContext.MemoryTasks))
		b.WriteString("\nskills_index: ")
		b.WriteString(formatYesNo(st.AttachedContext.SkillsIndexAttached))
		b.WriteString("\ninvoked_skills: ")
		b.WriteString(strconv.Itoa(st.AttachedContext.InvokedSkills))
		b.WriteString("\nestimated_attached_tokens: ")
		b.WriteString(strconv.Itoa(st.AttachedContext.EstimatedTokens))
		if st.AttachedContext.ContextLimit > 0 {
			b.WriteString(" / ")
			b.WriteString(strconv.Itoa(st.AttachedContext.ContextLimit))
			b.WriteString(fmt.Sprintf(" (%.1f%%)", st.AttachedContext.ContextPct))
		}
	} else {
		b.WriteString("unavailable")
	}
	b.WriteString("\n\nRecorded usage\n")
	if st.LastContextLimit > 0 {
		b.WriteString(statusBarLine("context", st.LastInputTokens, st.LastContextLimit))
		b.WriteString("\n")
	} else {
		b.WriteString("context  [")
		b.WriteString(strings.Repeat("░", 24))
		if st.UsageSource == "none" {
			b.WriteString("] unknown\n")
		} else {
			b.WriteString("] no limit\n")
		}
	}
	if st.UsageSource != "none" && st.TotalTokens > 0 {
		b.WriteString(statusBarLine("input", st.TotalInputTokens, st.TotalTokens))
		b.WriteString("\n")
		b.WriteString(statusBarLine("output", st.TotalOutputTokens, st.TotalTokens))
		b.WriteString("\n")
		b.WriteString(statusBarLine("total", st.TotalTokens, st.TotalTokens))
	} else {
		b.WriteString("input   [")
		b.WriteString(strings.Repeat("░", 24))
		b.WriteString("] no data\n")
		b.WriteString("output  [")
		b.WriteString(strings.Repeat("░", 24))
		b.WriteString("] no data\n")
		b.WriteString("total   [")
		b.WriteString(strings.Repeat("░", 24))
		if st.UsageSource == "none" {
			b.WriteString("] unknown")
		} else {
			b.WriteString("] no data")
		}
	}
	b.WriteString("\n```")
	return b.String()
}

func statusBarLine(label string, value, total int) string {
	const width = 24
	filled := 0
	if total > 0 {
		filled = int(math.Round(float64(value) / float64(total) * width))
	}
	if filled < 0 {
		filled = 0
	}
	if filled > width {
		filled = width
	}
	return fmt.Sprintf("%-6s [%s%s] %d", label, strings.Repeat("█", filled), strings.Repeat("░", width-filled), value)
}

func formatStatusTimeLocal(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Local().Format(time.RFC3339)
}

func formatStatusDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	return d.Round(time.Second).String()
}

func emptyDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}

func formatYesNo(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}

// refreshChatMetrics snapshots agent session + connector state for the status
// bar and header cluster. Cheap enough for the periodic poll; never per-keystroke.
func (m appModel) refreshChatMetrics() appModel {
	if m.user == nil || m.ag == nil {
		return m
	}
	m.connHealth = m.computeConnectorHealth()
	if m.conv == nil {
		return m
	}
	st := m.ag.SessionStatus(m.user.ID, m.conv.ID)
	m.chat = m.chat.withSessionMetrics(sessionMetrics{
		hasData:     st.UsageSource != "none",
		contextPct:  st.LastContextPct,
		contextOK:   st.LastContextLimit > 0,
		totalTokens: st.TotalTokens,
		lastModel:   st.LastModel,
		totalCost:   st.TotalCost,
		toolCalls:   st.TotalToolCalls,
		activeRuns:  m.activeRuns,
	})
	return m
}

// computeConnectorHealth derives connector + job state from config and recent
// audit activity. Uses only cheap count/latest queries (no live HTTP probes).
func (m appModel) computeConnectorHealth() connectorHealth {
	model := ""
	if m.ag != nil {
		model = m.ag.Model()
	}
	if m.ctx == nil {
		return connectorHealth{model: shortModel(model)}
	}
	return connectorHealthFor(m.ctx.DB, m.ctx.Config, model, m.ctx.ConnectorsLive)
}

// connectorHealthFor is the shared health computation used by both the chat
// header cluster and the Console strip. When live is false (terminal mode), no
// gateways are running in this process, so every connector reports offline.
func connectorHealthFor(db *sql.DB, cfg *config.Config, model string, live bool) connectorHealth {
	h := connectorHealth{model: shortModel(model)}
	if !live || db == nil || cfg == nil {
		return h
	}
	now := time.Now().UTC()

	// Signal: enabled + at least one linked number is healthy; enabled but
	// unlinked is degraded; disabled is off.
	if cfg.Signal.Enabled {
		h.signal = connWarn
		if linked, err := store.CountLinkedSignalNumbers(db); err == nil && linked > 0 {
			h.signal = connOnline
		}
	}

	// Discord: gateway runs when enabled.
	if cfg.Discord.Enabled {
		h.discord = connOnline
	}

	// Proactive jobs: recent tick = healthy, stale = degraded, never = off.
	if lastTick, ok, err := store.LatestAuditEventTime(db, "proactive_tick"); err == nil && ok {
		if now.Sub(lastTick.UTC()) < 10*time.Minute {
			h.jobs = connOnline
		} else {
			h.jobs = connWarn
		}
	}
	return h
}

func (m appModel) handleAgentReply(msg agentReplyMsg) (appModel, tea.Cmd) {
	if m.activeRuns > 0 {
		m.activeRuns--
	}
	delete(m.releasedRuns, msg.RequestID)
	targetConvID := int64(0)
	if msg.ConversationID != 0 {
		targetConvID = msg.ConversationID
	} else if m.conv != nil {
		targetConvID = m.conv.ID
	}
	renderInActiveChat := m.conv != nil && targetConvID != 0 && m.conv.ID == targetConvID
	matchedStreamRows := map[int]bool{}
	for _, tc := range msg.ToolCalls {
		if !isValidToolName(tc.Name) {
			continue
		}
		content := formatToolCallContent(tc)
		if targetConvID != 0 && !renderInActiveChat {
			_ = store.AddMessage(m.ctx.DB, targetConvID, "tool_call", content)
		}
		if renderInActiveChat {
			if idx, ok := m.chat.findStreamingToolCallRowIncludingCompleted(msg.RequestID, tc, matchedStreamRows); ok {
				matchedStreamRows[idx] = true
				m.chat = m.chat.updateMessageContent(idx, formatToolCallContent(tc))
			} else {
				m.chat = m.chat.upsertStreamingToolCall(msg.RequestID, tc)
			}
		}
	}

	clean, findings := redact.ScanAndRedact(msg.Text)
	if len(findings) > 0 {
		warn := "The assistant response contained secret-like content and was redacted."
		if targetConvID != 0 {
			_ = store.AddMessage(m.ctx.DB, targetConvID, "assistant", warn)
		}
		if renderInActiveChat {
			m.chat = m.chat.appendLocal("System", warn)
		}
	}
	if strings.TrimSpace(clean) == "" {
		if renderInActiveChat {
			m.chat = m.chat.finishStreamingAssistant(msg.RequestID, "", msg.Reasoning)
		}
		cmd := m.maybeDispatchWaitlist()
		return m.refreshChatMetrics(), cmd
	}
	if targetConvID != 0 && !renderInActiveChat {
		_, _ = store.AddAssistantMessageWithReasoning(m.ctx.DB, targetConvID, clean, msg.Model, msg.ReasoningItems)
	}
	if renderInActiveChat {
		m.chat = m.chat.finishStreamingAssistant(msg.RequestID, clean, msg.Reasoning)
		m.chat = m.chat.persistReasoningForRequest(msg.RequestID, msg.Model, msg.ReasoningItems)
	}
	cmd := m.maybeDispatchWaitlist()
	return m.refreshChatMetrics(), cmd
}

func (m *appModel) maybeDispatchWaitlist() tea.Cmd {
	if len(m.waitlist) == 0 {
		return nil
	}
	if m.user != nil && m.conv != nil && m.ag.HasPendingConfirmation(m.user.ID, m.conv.ID) {
		return nil
	}
	if m.activeRuns > 0 {
		return nil
	}
	return m.dispatchNextWaitlist()
}

func (m *appModel) dispatchNextWaitlist() tea.Cmd {
	if len(m.waitlist) == 0 {
		return nil
	}
	text := m.waitlist[0]
	m.waitlist = m.waitlist[1:]
	requestID := m.nextRequestID
	m.nextRequestID++
	m.activeRuns++
	m.releasedRuns[requestID] = false
	m.chat = m.chat.startStreamingAssistant(requestID)
	return tea.Batch(m.askAgentCmdWithID(requestID, text), m.chat.streamTickCmd())
}

func (m *appModel) releaseWaitlistFor(requestID int) tea.Cmd {
	if m.releasedRuns[requestID] {
		return nil
	}
	m.releasedRuns[requestID] = true
	if len(m.waitlist) == 0 {
		return nil
	}
	return m.dispatchNextWaitlist()
}

func (m appModel) triggerProactiveEventCmd(event string, meta map[string]string) tea.Cmd {
	if m.user == nil || m.proEng == nil {
		return nil
	}
	uid := m.user.ID
	eng := m.proEng
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		eng.TriggerEvent(ctx, uid, event, meta)
		return nil
	}
}

func (m appModel) triggerProactiveActionCmd(action string, meta map[string]string) tea.Cmd {
	if m.user == nil || m.proEng == nil {
		return nil
	}
	uid := m.user.ID
	eng := m.proEng
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		eng.TriggerAction(ctx, uid, action, meta)
		return nil
	}
}

func (m appModel) triggerProactiveAgentCmd(agentID string, meta map[string]string) tea.Cmd {
	if m.user == nil || m.proEng == nil {
		return nil
	}
	uid := m.user.ID
	eng := m.proEng
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		eng.TriggerAgent(ctx, uid, agentID, meta)
		return nil
	}
}

func (m appModel) backendSyncTickCmd() tea.Cmd {
	if m.ctx == nil || m.ctx.DB == nil || m.user == nil {
		return nil
	}
	return tea.Tick(4*time.Second, func(time.Time) tea.Msg { return appBackendSyncPollMsg{} })
}

func (m appModel) backendSyncNowCmd() tea.Cmd {
	if m.ctx == nil || m.ctx.DB == nil || m.user == nil {
		return nil
	}
	db := m.ctx.DB
	userID := m.user.ID
	currentConvID := int64(0)
	if m.conv != nil {
		currentConvID = m.conv.ID
	}
	knownLatestID := m.chat.latestPersistedMessageID()
	hasStreaming := m.chat.hasStreamingMessages() || m.activeRuns > 0
	return func() tea.Msg {
		conv, err := store.GetOrCreateActiveConversation(db, userID)
		if err != nil || conv == nil {
			return nil
		}
		if hasStreaming {
			return nil
		}
		if currentConvID != 0 && conv.ID == currentConvID {
			latestID, ok, err := store.LatestMessageID(db, conv.ID)
			if err != nil {
				return nil
			}
			if (!ok && knownLatestID == 0) || (ok && latestID <= knownLatestID) {
				return nil
			}
		}
		msgs, err := loadChatMessages(db, conv.ID, 200)
		if err != nil {
			return nil
		}
		return appBackendSyncMsg{Conversation: conv, Messages: msgs}
	}
}
