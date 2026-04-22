package tui

import (
	"context"
	"fmt"
	"math"
	"net/http"
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
	"tether/internal/personality"
	"tether/internal/proactive"
	"tether/internal/redact"
	"tether/internal/secrets"
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

func (m appModel) Init() tea.Cmd {
	return tea.Batch(
		tea.RequestBackgroundColor,
		cursor.Blink,
	)
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
		conv, err := store.GetOrCreateActiveConversation(m.ctx.DB, u.ID)
		if err != nil {
			m.auth, _ = m.auth.Update(authStatusMsg{Text: err.Error(), IsErr: true})
			return m, nil
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
				msg := "[Proactive/" + n.Kind + "] " + n.Content
				_ = store.AddMessage(m.ctx.DB, targetConvID, "system", msg)
				_ = store.MarkNotificationDelivered(m.ctx.DB, n.ID)
			}
		}

		// Ensure per-user sandbox/config directories exist.
		dirs := userspace.ForUser(m.ctx.Config.Paths.DataDir, u.ID)
		if err := userspace.Ensure(dirs); err != nil {
			m.auth, _ = m.auth.Update(authStatusMsg{Text: err.Error(), IsErr: true})
			return m, nil
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
		return m, tea.Batch(
			m.chat.loadCmd(),
			m.triggerProactiveEventCmd(proactive.EventLogin, nil),
			m.backendSyncTickCmd(),
		)

	case loginSuccessMsg:
		m.user = msg.User
		m = m.activateConversation(msg.Conv)
		m.memory = m.memory.withUser(m.ctx.DB, m.user.ID).withSize(m.w, m.h-1)
		m.settings = m.settings.withUser(m.user.ID).withSize(m.w, m.h-1)
		return m, tea.Batch(
			m.chat.loadCmd(),
			m.triggerProactiveEventCmd(proactive.EventLogin, nil),
			m.backendSyncTickCmd(),
		)

	case appBackendSyncPollMsg:
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

	// Right side: terminal compatibility notice + online dot + username.
	if label := strings.TrimSpace(term.HeaderLabel); label != "" {
		userBadge = styleHeaderNotice.Render(label)
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
		btns = append(btns, headerButton{ID: "admin", Label: "Admin"})
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

func (m appModel) handleCommand(text string) (appModel, bool, tea.Cmd) {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return m, true, nil
	}

	switch fields[0] {
	case "/status":
		if m.conv == nil || m.user == nil {
			return m, true, nil
		}
		_ = store.AddMessage(m.ctx.DB, m.conv.ID, "user", text)
		m.chat = m.chat.appendLocal("You", text)
		resp := renderSessionStatusTUI(m.ag.SessionStatus(m.user.ID, m.conv.ID))
		_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
		m.chat = m.chat.appendLocal("System", resp)
		return m, true, nil

	case "/clear":
		if m.conv == nil || m.user == nil {
			return m, true, nil
		}
		oldConv := m.conv
		resumeCode := store.EncodeResumeCode(oldConv.ID)
		_ = store.AddMessage(m.ctx.DB, oldConv.ID, "user", text)
		m.chat = m.chat.appendLocal("You", text)

		newConv, err := store.CreateConversation(m.ctx.DB, m.user.ID, "")
		if err != nil {
			resp := "failed to clear chat: " + err.Error()
			_ = store.AddMessage(m.ctx.DB, oldConv.ID, "assistant", resp)
			m.chat = m.chat.appendLocal("System", resp)
			return m, true, nil
		}
		if err := store.SetActiveConversation(m.ctx.DB, m.user.ID, newConv.ID); err != nil {
			resp := "failed to switch chat: " + err.Error()
			_ = store.AddMessage(m.ctx.DB, oldConv.ID, "assistant", resp)
			m.chat = m.chat.appendLocal("System", resp)
			return m, true, nil
		}
		m.ag.ResetConversationSession(newConv.ID)
		m = m.activateConversation(newConv)
		resp := "Started a fresh conversation with a clean agent context. Resume the previous chat with `/resume " + resumeCode + "`."
		_ = store.AddMessage(m.ctx.DB, newConv.ID, "assistant", resp)
		m.chat = m.chat.appendLocal("System", resp)
		return m, true, m.chat.loadCmd()

	case "/resume":
		if m.conv == nil || m.user == nil {
			return m, true, nil
		}
		if len(fields) != 2 {
			resp := "usage: /resume <code>"
			_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
			m.chat = m.chat.appendLocal("System", resp)
			return m, true, nil
		}
		_ = store.AddMessage(m.ctx.DB, m.conv.ID, "user", text)
		m.chat = m.chat.appendLocal("You", text)
		convID, err := store.DecodeResumeCode(fields[1])
		if err != nil {
			resp := err.Error()
			_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
			m.chat = m.chat.appendLocal("System", resp)
			return m, true, nil
		}
		conv, ok, err := store.GetConversation(m.ctx.DB, m.user.ID, convID)
		if err != nil {
			resp := "failed to resume chat: " + err.Error()
			_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
			m.chat = m.chat.appendLocal("System", resp)
			return m, true, nil
		}
		if !ok {
			resp := "conversation not found for that resume code"
			_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
			m.chat = m.chat.appendLocal("System", resp)
			return m, true, nil
		}
		if err := store.SetActiveConversation(m.ctx.DB, m.user.ID, conv.ID); err != nil {
			resp := "failed to switch chat: " + err.Error()
			_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
			m.chat = m.chat.appendLocal("System", resp)
			return m, true, nil
		}
		m = m.activateConversation(conv)
		return m, true, m.chat.loadCmd()

	case "/confirm":
		if m.conv == nil || m.user == nil {
			return m, true, nil
		}
		if len(fields) != 2 {
			resp := "usage: /confirm <token>"
			_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
			m.chat = m.chat.appendLocal("System", resp)
			return m, true, nil
		}
		_ = store.AddMessage(m.ctx.DB, m.conv.ID, "user", text)
		m.chat = m.chat.appendLocal("You", text)
		requestID := m.nextRequestID
		m.nextRequestID++
		m.activeRuns++
		m.releasedRuns[requestID] = false
		m.chat = m.chat.startStreamingAssistant(requestID)
		return m, true, tea.Batch(m.resumeConfirmationCmd(requestID, fields[1]), m.chat.streamTickCmd())

	case "/admin":
		if m.conv == nil || m.user == nil {
			return m, true, nil
		}
		if m.user.Role != "admin" {
			resp := "admin only"
			_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
			m.chat = m.chat.appendLocal("System", resp)
			return m, true, nil
		}
		if len(fields) < 2 {
			resp := usageBlock(
				"/admin users list",
				"/admin users promote <username>",
				"/admin users demote <username>",
				"/admin audit tail [n]",
				"/admin signal status",
				"/admin jobs status",
			)
			_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
			m.chat = m.chat.appendLocal("System", resp)
			return m, true, nil
		}
		section := fields[1]
		switch section {
		case "users":
			if len(fields) < 3 {
				resp := usageBlock(
					"/admin users list",
					"/admin users promote <username>",
					"/admin users demote <username>",
				)
				_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
				m.chat = m.chat.appendLocal("System", resp)
				return m, true, nil
			}
			sub := fields[2]
			switch sub {
			case "list":
				_ = store.AddMessage(m.ctx.DB, m.conv.ID, "user", text)
				m.chat = m.chat.appendLocal("You", text)
				users, err := store.ListUsers(m.ctx.DB)
				if err != nil {
					resp := "failed: " + err.Error()
					_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
					m.chat = m.chat.appendLocal("System", resp)
					return m, true, nil
				}
				var b strings.Builder
				b.WriteString("Users:\n")
				for _, u := range users {
					b.WriteString("- ")
					b.WriteString(u.Username)
					b.WriteString(" (")
					b.WriteString(u.Role)
					b.WriteString(")\n")
				}
				resp := strings.TrimSpace(b.String())
				_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
				m.chat = m.chat.appendLocal("System", resp)
				return m, true, nil

			case "promote", "demote":
				if len(fields) < 4 {
					resp := usageBlock(
						"/admin users promote <username>",
						"/admin users demote <username>",
					)
					_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
					m.chat = m.chat.appendLocal("System", resp)
					return m, true, nil
				}
				user := fields[3]
				role := "user"
				if sub == "promote" {
					role = "admin"
				}
				_ = store.AddMessage(m.ctx.DB, m.conv.ID, "user", text)
				m.chat = m.chat.appendLocal("You", text)
				if err := store.SetUserRole(m.ctx.DB, user, role); err != nil {
					resp := "failed: " + err.Error()
					_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
					m.chat = m.chat.appendLocal("System", resp)
					return m, true, nil
				}
				resp := "updated role for " + user + " to " + role
				_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
				m.chat = m.chat.appendLocal("System", resp)
				return m, true, nil
			}
			resp := usageBlock(
				"/admin users list",
				"/admin users promote <username>",
				"/admin users demote <username>",
			)
			_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
			m.chat = m.chat.appendLocal("System", resp)
			return m, true, nil
		case "audit":
			if len(fields) < 3 || fields[2] != "tail" {
				resp := "usage: /admin audit tail [n]"
				_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
				m.chat = m.chat.appendLocal("System", resp)
				return m, true, nil
			}
			_ = store.AddMessage(m.ctx.DB, m.conv.ID, "user", text)
			m.chat = m.chat.appendLocal("You", text)
			limit := 50
			if len(fields) >= 4 {
				if n, err := strconv.Atoi(fields[3]); err == nil {
					limit = n
				}
			}
			evs, err := store.ListAuditEvents(m.ctx.DB, limit)
			if err != nil {
				resp := "failed: " + err.Error()
				_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
				m.chat = m.chat.appendLocal("System", resp)
				return m, true, nil
			}
			var b strings.Builder
			b.WriteString("Audit events (latest first):\n")
			for _, ev := range evs {
				b.WriteString("- ")
				b.WriteString(ev.CreatedAt.UTC().Format(time.RFC3339))
				b.WriteString(" ")
				b.WriteString(ev.Type)
				if ev.UserID != nil {
					b.WriteString(" user=")
					b.WriteString(fmt.Sprintf("%d", *ev.UserID))
				}
				if ev.Payload != "" {
					b.WriteString(" ")
					b.WriteString(ev.Payload)
				}
				b.WriteString("\n")
			}
			resp := strings.TrimSpace(b.String())
			_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
			m.chat = m.chat.appendLocal("System", resp)
			return m, true, nil

		case "signal":
			_ = store.AddMessage(m.ctx.DB, m.conv.ID, "user", text)
			m.chat = m.chat.appendLocal("You", text)
			if len(fields) < 3 || fields[2] != "status" {
				resp := "usage: /admin signal status"
				_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
				m.chat = m.chat.appendLocal("System", resp)
				return m, true, nil
			}
			if !m.ctx.Config.Signal.Enabled {
				resp := "Signal: disabled"
				_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
				m.chat = m.chat.appendLocal("System", resp)
				return m, true, nil
			}
			addr := strings.TrimSpace(m.ctx.Config.Signal.HTTPAddr)
			base := addr
			if !strings.HasPrefix(base, "http") {
				base = "http://" + base
			}
			req, _ := http.NewRequest(http.MethodGet, base+"/api/v1/check", nil)
			resp2, err := http.DefaultClient.Do(req)
			if err != nil {
				resp := "Signal: enabled (check failed: " + err.Error() + ")"
				_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
				m.chat = m.chat.appendLocal("System", resp)
				return m, true, nil
			}
			resp2.Body.Close()

			linked, _ := store.CountLinkedSignalNumbers(m.ctx.DB)
			lastIn, okIn, _ := store.LatestAuditEventTime(m.ctx.DB, "signal_inbound")
			lastOut, okOut, _ := store.LatestAuditEventTime(m.ctx.DB, "signal_send")
			li := "(never)"
			lo := "(never)"
			if okIn {
				li = lastIn.UTC().Format(time.RFC3339)
			}
			if okOut {
				lo = lastOut.UTC().Format(time.RFC3339)
			}

			resp := fmt.Sprintf("Signal status:\n- enabled: true\n- account: %s\n- http: %s\n- check_status: %d\n- linked_numbers: %d\n- last_inbound_utc: %s\n- last_send_utc: %s", m.ctx.Config.Signal.AccountNumber, addr, resp2.StatusCode, linked, li, lo)
			_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
			m.chat = m.chat.appendLocal("System", resp)
			return m, true, nil

		case "jobs":
			_ = store.AddMessage(m.ctx.DB, m.conv.ID, "user", text)
			m.chat = m.chat.appendLocal("You", text)
			if len(fields) < 3 || fields[2] != "status" {
				resp := "usage: /admin jobs status"
				_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
				m.chat = m.chat.appendLocal("System", resp)
				return m, true, nil
			}
			lastTick, ok, err := store.LatestAuditEventTime(m.ctx.DB, "proactive_tick")
			if err != nil {
				resp := "failed: " + err.Error()
				_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
				m.chat = m.chat.appendLocal("System", resp)
				return m, true, nil
			}
			undelivered, _ := store.CountUndeliveredNotifications(m.ctx.DB)
			lt := "(never)"
			if ok {
				lt = lastTick.UTC().Format(time.RFC3339)
			}

			lastTrig, okT, _ := store.LatestAuditEventTime(m.ctx.DB, "proactive_trigger")
			lastSkip, okS, _ := store.LatestAuditEventTime(m.ctx.DB, "proactive_skip_rate_limit")
			ltt := "(never)"
			lts := "(never)"
			if okT {
				ltt = lastTrig.UTC().Format(time.RFC3339)
			}
			if okS {
				lts = lastSkip.UTC().Format(time.RFC3339)
			}

			nDaily, okD, _ := store.LatestNotificationTime(m.ctx.DB, "daily_brief")
			nOpen, okO, _ := store.LatestNotificationTime(m.ctx.DB, "open_loops")
			nInact, okI, _ := store.LatestNotificationTime(m.ctx.DB, "inactivity_nudge")
			daily := "(never)"
			open := "(never)"
			inact := "(never)"
			if okD {
				daily = nDaily.UTC().Format(time.RFC3339)
			}
			if okO {
				open = nOpen.UTC().Format(time.RFC3339)
			}
			if okI {
				inact = nInact.UTC().Format(time.RFC3339)
			}

			resp := fmt.Sprintf("Jobs status:\n- proactive_tick_last_utc: %s\n- proactive_trigger_last_utc: %s\n- proactive_rate_limit_skip_last_utc: %s\n- latest_daily_brief_utc: %s\n- latest_open_loops_utc: %s\n- latest_inactivity_nudge_utc: %s\n- undelivered_notifications: %d", lt, ltt, lts, daily, open, inact, undelivered)
			_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
			m.chat = m.chat.appendLocal("System", resp)
			return m, true, nil
		}

		resp := usageBlock(
			"/admin users list",
			"/admin users promote <username>",
			"/admin users demote <username>",
			"/admin audit tail [n]",
			"/admin signal status",
			"/admin jobs status",
		)
		_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
		m.chat = m.chat.appendLocal("System", resp)
		return m, true, nil

	case "/help":
		if m.conv != nil {
			_ = store.AddMessage(m.ctx.DB, m.conv.ID, "user", text)
			m.chat = m.chat.appendLocal("You", text)
			resp := "Commands:\n" +
				"  /status\n" +
				"  /clear\n" +
				"  /resume <code>\n" +
				"  /help\n" +
				"  /logout\n" +
				"  /tools list\n" +
				"  /tools search <query>\n" +
				"  /tools describe <name>\n" +
				"  /subagent spawn <prompt>\n" +
				"  /subagent status <id>\n" +
				"  /proactive action <name>\n" +
				"  /proactive agent <id>\n" +
				"  /signal link\n" +
				"  /signal status\n" +
				"  /signal unlink\n" +
				"  /discord status\n" +
				"  /discord link <code>\n" +
				"  /discord unlink\n" +
				"  /confirm <token>\n" +
				"  /memory list [kind]\n" +
				"  /memory add <kind> <content>\n" +
				"  /memory delete <id>\n" +
				"  /memory update <id> <content>\n" +
				"  /task list\n" +
				"  /task add <text>\n" +
				"  /task edit <id> <text>\n" +
				"  /task done <id>\n" +
				"  /secret add <label> <secret>\n" +
				"  /secret list\n" +
				"  /secret delete <label>\n" +
				"  /secret clear\n"
			if m.user != nil && m.user.Role == "admin" {
				resp +=
					"  /admin users list (admin)\n" +
						"  /admin audit tail [n] (admin)\n" +
						"  /admin signal status (admin)\n" +
						"  /admin jobs status (admin)\n"
			}
			_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
			m.chat = m.chat.appendLocal("System", resp)
		}
		return m, true, nil

	case "/tools":
		if m.conv == nil {
			return m, true, nil
		}
		_ = store.AddMessage(m.ctx.DB, m.conv.ID, "user", text)
		m.chat = m.chat.appendLocal("You", text)

		if len(fields) > 1 && fields[1] == "describe" {
			if len(fields) < 3 {
				resp := "usage: /tools describe <name>"
				_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
				m.chat = m.chat.appendLocal("System", resp)
				return m, true, nil
			}
			name := strings.TrimSpace(fields[2])
			spec, ok := m.toolReg.Get(name)
			if !ok {
				resp := "unknown tool: " + name
				_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
				m.chat = m.chat.appendLocal("System", resp)
				return m, true, nil
			}
			resp := strings.TrimSpace(tools.RenderToolMarkdown(spec))
			_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
			m.chat = m.chat.appendLocal("System", resp)
			return m, true, nil
		}

		var infos []tools.ToolInfo
		if len(fields) == 1 || fields[1] == "list" {
			infos = m.toolReg.List()
		} else if fields[1] == "search" {
			q := ""
			if len(fields) > 2 {
				q = strings.Join(fields[2:], " ")
			}
			infos = m.toolReg.Search(q)
		} else {
			resp := usageBlock(
				"/tools list",
				"/tools search <query>",
				"/tools describe <name>",
			)
			_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
			m.chat = m.chat.appendLocal("System", resp)
			return m, true, nil
		}

		var b strings.Builder
		b.WriteString("Tools:\n")
		for _, t := range infos {
			b.WriteString("- ")
			b.WriteString(t.Name)
			if t.Description != "" {
				b.WriteString(" — ")
				b.WriteString(t.Description)
			}
			b.WriteString("\n")
		}
		resp := strings.TrimSpace(b.String())
		_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
		m.chat = m.chat.appendLocal("System", resp)
		return m, true, nil

	case "/signal":
		if m.conv == nil || m.user == nil {
			return m, true, nil
		}
		if len(fields) < 2 {
			resp := usageBlock(
				"/signal link",
				"/signal status",
				"/signal unlink",
			)
			_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
			m.chat = m.chat.appendLocal("System", resp)
			return m, true, nil
		}
		sub := fields[1]
		switch sub {
		case "link":
			_ = store.AddMessage(m.ctx.DB, m.conv.ID, "user", text)
			m.chat = m.chat.appendLocal("You", text)
			code, err := store.CreateSignalLinkCode(m.ctx.DB, m.user.ID, 10*time.Minute)
			if err != nil {
				resp := "failed to create link code: " + err.Error()
				_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
				m.chat = m.chat.appendLocal("System", resp)
				return m, true, nil
			}
			acct := strings.TrimSpace(m.ctx.Config.Signal.AccountNumber)
			if acct == "" {
				acct = "<signal account not configured>"
			}
			resp := "Signal link code: " + code + "\nSend this code from your phone number to the Tether Signal account: " + acct + "\n(Code expires in ~10 minutes.)"
			_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
			m.chat = m.chat.appendLocal("System", resp)
			return m, true, nil

		case "status":
			_ = store.AddMessage(m.ctx.DB, m.conv.ID, "user", text)
			m.chat = m.chat.appendLocal("You", text)
			n, ok, err := store.GetSignalNumber(m.ctx.DB, m.user.ID)
			if err != nil {
				resp := "failed to get signal status: " + err.Error()
				_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
				m.chat = m.chat.appendLocal("System", resp)
				return m, true, nil
			}
			if !ok {
				resp := "Signal: not linked"
				_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
				m.chat = m.chat.appendLocal("System", resp)
				return m, true, nil
			}
			resp := "Signal linked: " + n
			_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
			m.chat = m.chat.appendLocal("System", resp)
			return m, true, nil

		case "unlink":
			_ = store.AddMessage(m.ctx.DB, m.conv.ID, "user", text)
			m.chat = m.chat.appendLocal("You", text)
			if err := store.UnlinkSignalNumber(m.ctx.DB, m.user.ID); err != nil {
				resp := "failed to unlink: " + err.Error()
				_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
				m.chat = m.chat.appendLocal("System", resp)
				return m, true, nil
			}
			resp := "Signal unlinked"
			_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
			m.chat = m.chat.appendLocal("System", resp)
			return m, true, nil
		}

		resp := usageBlock(
			"/signal link",
			"/signal status",
			"/signal unlink",
		)
		_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
		m.chat = m.chat.appendLocal("System", resp)
		return m, true, nil

	case "/discord":
		if m.conv == nil || m.user == nil {
			return m, true, nil
		}
		if len(fields) < 2 {
			resp := usageBlock(
				"/discord status",
				"/discord link <code>",
				"/discord unlink",
			)
			_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
			m.chat = m.chat.appendLocal("System", resp)
			return m, true, nil
		}
		sub := fields[1]
		switch sub {
		case "status":
			_ = store.AddMessage(m.ctx.DB, m.conv.ID, "user", text)
			m.chat = m.chat.appendLocal("You", text)
			did, ok, err := store.GetDiscordUserID(m.ctx.DB, m.user.ID)
			if err != nil {
				resp := "failed to get discord status: " + err.Error()
				_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
				m.chat = m.chat.appendLocal("System", resp)
				return m, true, nil
			}
			if !ok {
				resp := "Discord: not linked"
				_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
				m.chat = m.chat.appendLocal("System", resp)
				return m, true, nil
			}
			resp := "Discord linked: " + did
			_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
			m.chat = m.chat.appendLocal("System", resp)
			return m, true, nil

		case "unlink":
			_ = store.AddMessage(m.ctx.DB, m.conv.ID, "user", text)
			m.chat = m.chat.appendLocal("You", text)
			if err := store.UnlinkDiscordUserID(m.ctx.DB, m.user.ID); err != nil {
				resp := "failed to unlink: " + err.Error()
				_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
				m.chat = m.chat.appendLocal("System", resp)
				return m, true, nil
			}
			resp := "Discord unlinked"
			_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
			m.chat = m.chat.appendLocal("System", resp)
			return m, true, nil

		case "link":
			if len(fields) < 3 {
				resp := "usage: /discord link <code>"
				_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
				m.chat = m.chat.appendLocal("System", resp)
				return m, true, nil
			}
			_ = store.AddMessage(m.ctx.DB, m.conv.ID, "user", text)
			m.chat = m.chat.appendLocal("You", text)

			if _, ok, err := store.GetDiscordUserID(m.ctx.DB, m.user.ID); err != nil {
				resp := "failed to get discord status: " + err.Error()
				_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
				m.chat = m.chat.appendLocal("System", resp)
				return m, true, nil
			} else if ok {
				resp := "Discord already linked. Run /discord unlink first."
				_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
				m.chat = m.chat.appendLocal("System", resp)
				return m, true, nil
			}

			code := strings.TrimSpace(fields[2])
			discordUID, ok, err := store.ConsumeDiscordLinkCode(m.ctx.DB, code)
			if err != nil {
				resp := "failed to consume link code: " + err.Error()
				_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
				m.chat = m.chat.appendLocal("System", resp)
				return m, true, nil
			}
			if !ok {
				resp := "invalid or expired link code"
				_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
				m.chat = m.chat.appendLocal("System", resp)
				return m, true, nil
			}

			if otherUID, ok2, err := store.FindUserIDByDiscordUserID(m.ctx.DB, discordUID); err != nil {
				resp := "failed to check discord link: " + err.Error()
				_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
				m.chat = m.chat.appendLocal("System", resp)
				return m, true, nil
			} else if ok2 {
				if otherUID == m.user.ID {
					resp := "Discord already linked."
					_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
					m.chat = m.chat.appendLocal("System", resp)
					return m, true, nil
				}
				resp := "That Discord account is already linked to another Tether user."
				_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
				m.chat = m.chat.appendLocal("System", resp)
				return m, true, nil
			}

			if err := store.LinkDiscordUserID(m.ctx.DB, m.user.ID, discordUID); err != nil {
				if err == store.ErrDiscordAlreadyLinkedForUser {
					resp := "Discord already linked. Run /discord unlink first."
					_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
					m.chat = m.chat.appendLocal("System", resp)
					return m, true, nil
				}
				resp := "failed to link discord: " + err.Error()
				_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
				m.chat = m.chat.appendLocal("System", resp)
				return m, true, nil
			}

			resp := "Discord linked."
			_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
			m.chat = m.chat.appendLocal("System", resp)
			return m, true, nil
		}

		resp := usageBlock(
			"/discord status",
			"/discord link <code>",
			"/discord unlink",
		)
		_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
		m.chat = m.chat.appendLocal("System", resp)
		return m, true, nil

	case "/memory":
		if m.conv == nil || m.user == nil {
			return m, true, nil
		}
		if len(fields) < 2 {
			resp := usageBlock(
				"/memory list [kind]",
				"/memory add <kind> <content>",
				"/memory update <id> <content>",
				"/memory delete <id>",
			)
			_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
			m.chat = m.chat.appendLocal("System", resp)
			return m, true, nil
		}
		sub := fields[1]
		switch sub {
		case "list":
			_ = store.AddMessage(m.ctx.DB, m.conv.ID, "user", text)
			m.chat = m.chat.appendLocal("You", text)
			kind := ""
			if len(fields) >= 3 {
				kind = fields[2]
			}
			items, err := store.ListMemoryItems(m.ctx.DB, m.user.ID, kind, 100)
			if err != nil {
				resp := "failed to list memory: " + err.Error()
				_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
				m.chat = m.chat.appendLocal("System", resp)
				return m, true, nil
			}
			if len(items) == 0 {
				resp := "no memory items"
				_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
				m.chat = m.chat.appendLocal("System", resp)
				return m, true, nil
			}
			var b strings.Builder
			b.WriteString("Memory:\n")
			for _, it := range items {
				b.WriteString("- ")
				b.WriteString(fmt.Sprintf("%d", it.ID))
				b.WriteString(" [")
				b.WriteString(it.Kind)
				b.WriteString("] ")
				b.WriteString(it.Content)
				b.WriteString("\n")
			}
			resp := strings.TrimSpace(b.String())
			_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
			m.chat = m.chat.appendLocal("System", resp)
			return m, true, nil

		case "add":
			if len(fields) < 4 {
				resp := "usage: /memory add <kind> <content>"
				_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
				m.chat = m.chat.appendLocal("System", resp)
				return m, true, nil
			}
			kind := fields[2]
			content := strings.Join(fields[3:], " ")
			_ = store.AddMessage(m.ctx.DB, m.conv.ID, "user", text)
			m.chat = m.chat.appendLocal("You", text)
			id, err := store.AddMemoryItem(m.ctx.DB, m.user.ID, kind, content)
			if err != nil {
				resp := "failed to add memory: " + err.Error()
				_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
				m.chat = m.chat.appendLocal("System", resp)
				return m, true, nil
			}
			resp := "memory added (id " + fmt.Sprintf("%d", id) + ")"
			_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
			m.chat = m.chat.appendLocal("System", resp)
			return m, true, nil

		case "update":
			if len(fields) < 4 {
				resp := "usage: /memory update <id> <content>"
				_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
				m.chat = m.chat.appendLocal("System", resp)
				return m, true, nil
			}
			_ = store.AddMessage(m.ctx.DB, m.conv.ID, "user", text)
			m.chat = m.chat.appendLocal("You", text)
			id, err := strconv.ParseInt(fields[2], 10, 64)
			if err != nil {
				resp := "invalid id"
				_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
				m.chat = m.chat.appendLocal("System", resp)
				return m, true, nil
			}
			content := strings.Join(fields[3:], " ")
			if err := store.UpdateMemoryItem(m.ctx.DB, m.user.ID, id, content); err != nil {
				resp := "failed to update memory: " + err.Error()
				_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
				m.chat = m.chat.appendLocal("System", resp)
				return m, true, nil
			}
			resp := "memory updated"
			_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
			m.chat = m.chat.appendLocal("System", resp)
			return m, true, nil

		case "delete":
			if len(fields) < 3 {
				resp := "usage: /memory delete <id>"
				_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
				m.chat = m.chat.appendLocal("System", resp)
				return m, true, nil
			}
			_ = store.AddMessage(m.ctx.DB, m.conv.ID, "user", text)
			m.chat = m.chat.appendLocal("You", text)
			id, err := strconv.ParseInt(fields[2], 10, 64)
			if err != nil {
				resp := "invalid id"
				_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
				m.chat = m.chat.appendLocal("System", resp)
				return m, true, nil
			}
			if err := store.DeleteMemoryItem(m.ctx.DB, m.user.ID, id); err != nil {
				resp := "failed to delete memory: " + err.Error()
				_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
				m.chat = m.chat.appendLocal("System", resp)
				return m, true, nil
			}
			resp := "memory deleted"
			_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
			m.chat = m.chat.appendLocal("System", resp)
			return m, true, nil
		}

		resp := usageBlock(
			"/memory list [kind]",
			"/memory add <kind> <content>",
			"/memory update <id> <content>",
			"/memory delete <id>",
		)
		_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
		m.chat = m.chat.appendLocal("System", resp)
		return m, true, nil

	case "/task":
		if m.conv == nil || m.user == nil {
			return m, true, nil
		}
		if len(fields) < 2 {
			resp := usageBlock(
				"/task list",
				"/task add <text>",
				"/task edit <id> <text>",
				"/task done <id>",
			)
			_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
			m.chat = m.chat.appendLocal("System", resp)
			return m, true, nil
		}
		sub := fields[1]
		switch sub {
		case "list":
			_ = store.AddMessage(m.ctx.DB, m.conv.ID, "user", text)
			m.chat = m.chat.appendLocal("You", text)
			items, err := store.ListMemoryItems(m.ctx.DB, m.user.ID, "task", 100)
			if err != nil {
				resp := "failed: " + err.Error()
				_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
				m.chat = m.chat.appendLocal("System", resp)
				return m, true, nil
			}
			if len(items) == 0 {
				resp := "no tasks"
				_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
				m.chat = m.chat.appendLocal("System", resp)
				return m, true, nil
			}
			var b strings.Builder
			b.WriteString("Tasks:\n")
			for _, it := range items {
				b.WriteString("- ")
				b.WriteString(fmt.Sprintf("%d", it.ID))
				b.WriteString(": ")
				b.WriteString(it.Content)
				b.WriteString("\n")
			}
			resp := strings.TrimSpace(b.String())
			_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
			m.chat = m.chat.appendLocal("System", resp)
			return m, true, nil

		case "add":
			if len(fields) < 3 {
				resp := "usage: /task add <text>"
				_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
				m.chat = m.chat.appendLocal("System", resp)
				return m, true, nil
			}
			_ = store.AddMessage(m.ctx.DB, m.conv.ID, "user", text)
			m.chat = m.chat.appendLocal("You", text)
			content := strings.Join(fields[2:], " ")
			id, err := store.AddMemoryItem(m.ctx.DB, m.user.ID, "task", content)
			if err != nil {
				resp := "failed: " + err.Error()
				_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
				m.chat = m.chat.appendLocal("System", resp)
				return m, true, nil
			}
			resp := "task added (id " + fmt.Sprintf("%d", id) + ")"
			_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
			m.chat = m.chat.appendLocal("System", resp)
			return m, true, m.triggerProactiveEventCmd(proactive.EventTaskChanged, map[string]string{"text": content})

		case "edit":
			if len(fields) < 4 {
				resp := "usage: /task edit <id> <text>"
				_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
				m.chat = m.chat.appendLocal("System", resp)
				return m, true, nil
			}
			_ = store.AddMessage(m.ctx.DB, m.conv.ID, "user", text)
			m.chat = m.chat.appendLocal("You", text)
			id, err := strconv.ParseInt(fields[2], 10, 64)
			if err != nil {
				resp := "invalid id"
				_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
				m.chat = m.chat.appendLocal("System", resp)
				return m, true, nil
			}
			content := strings.Join(fields[3:], " ")
			if err := store.UpdateMemoryItem(m.ctx.DB, m.user.ID, id, content); err != nil {
				resp := "failed: " + err.Error()
				_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
				m.chat = m.chat.appendLocal("System", resp)
				return m, true, nil
			}
			resp := "task updated"
			_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
			m.chat = m.chat.appendLocal("System", resp)
			return m, true, m.triggerProactiveEventCmd(proactive.EventTaskChanged, map[string]string{"text": content})

		case "done":
			if len(fields) < 3 {
				resp := "usage: /task done <id>"
				_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
				m.chat = m.chat.appendLocal("System", resp)
				return m, true, nil
			}
			_ = store.AddMessage(m.ctx.DB, m.conv.ID, "user", text)
			m.chat = m.chat.appendLocal("You", text)
			id, err := strconv.ParseInt(fields[2], 10, 64)
			if err != nil {
				resp := "invalid id"
				_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
				m.chat = m.chat.appendLocal("System", resp)
				return m, true, nil
			}
			if err := store.DeleteMemoryItem(m.ctx.DB, m.user.ID, id); err != nil {
				resp := "failed: " + err.Error()
				_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
				m.chat = m.chat.appendLocal("System", resp)
				return m, true, nil
			}
			resp := "task marked done"
			_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
			m.chat = m.chat.appendLocal("System", resp)
			return m, true, m.triggerProactiveEventCmd(proactive.EventTaskChanged, map[string]string{"text": text})
		}

		resp := usageBlock(
			"/task list",
			"/task add <text>",
			"/task edit <id> <text>",
			"/task done <id>",
		)
		_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
		m.chat = m.chat.appendLocal("System", resp)
		return m, true, nil

	case "/secret":
		if m.conv == nil || m.user == nil {
			return m, true, nil
		}
		// NOTE: we never persist or display secret plaintext.
		if len(fields) < 2 {
			resp := usageBlock(
				"/secret add <label> <secret>",
				"/secret list",
				"/secret delete <label>",
				"/secret clear",
			)
			_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
			m.chat = m.chat.appendLocal("System", resp)
			return m, true, nil
		}

		s, err := secrets.NewStore(m.ctx.DB, m.ctx.Config.Secrets.MasterKey, time.Duration(m.ctx.Config.Secrets.TTLHours)*time.Hour)
		if err != nil {
			resp := "secrets unavailable: " + err.Error() + " (set TETHER_MASTER_KEY; generate with: go run ./cmd/tether-keygen)"
			_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
			m.chat = m.chat.appendLocal("System", resp)
			return m, true, nil
		}

		sub := fields[1]
		switch sub {
		case "add":
			if len(fields) < 4 {
				resp := "usage: /secret add <label> <secret>"
				_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
				m.chat = m.chat.appendLocal("System", resp)
				return m, true, nil
			}
			label := fields[2]
			secretText := strings.Join(fields[3:], " ")
			// Record command without the secret.
			redactedCmd := "/secret add " + label + " [REDACTED]"
			_ = store.AddMessage(m.ctx.DB, m.conv.ID, "user", redactedCmd)
			m.chat = m.chat.appendLocal("You", redactedCmd)

			if err := s.Put(context.Background(), m.user.ID, label, secretText); err != nil {
				resp := "failed to store secret: " + err.Error()
				_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
				m.chat = m.chat.appendLocal("System", resp)
				return m, true, nil
			}
			exp := time.Now().Add(time.Duration(m.ctx.Config.Secrets.TTLHours) * time.Hour).Format(time.RFC3339)
			resp := "secret stored as '" + label + "' (expires ~" + exp + ")"
			_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
			m.chat = m.chat.appendLocal("System", resp)
			return m, true, nil

		case "list":
			_ = store.AddMessage(m.ctx.DB, m.conv.ID, "user", text)
			m.chat = m.chat.appendLocal("You", text)
			items, err := s.List(context.Background(), m.user.ID)
			if err != nil {
				resp := "failed to list secrets: " + err.Error()
				_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
				m.chat = m.chat.appendLocal("System", resp)
				return m, true, nil
			}
			if len(items) == 0 {
				resp := "no secrets set"
				_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
				m.chat = m.chat.appendLocal("System", resp)
				return m, true, nil
			}
			var b strings.Builder
			b.WriteString("Secrets (labels only):\n")
			for _, it := range items {
				b.WriteString("- ")
				b.WriteString(it.Label)
				b.WriteString(" (expires ")
				b.WriteString(it.ExpiresAt.Format(time.RFC3339))
				b.WriteString(")\n")
			}
			resp := strings.TrimSpace(b.String())
			_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
			m.chat = m.chat.appendLocal("System", resp)
			return m, true, nil

		case "delete":
			if len(fields) < 3 {
				resp := "usage: /secret delete <label>"
				_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
				m.chat = m.chat.appendLocal("System", resp)
				return m, true, nil
			}
			label := fields[2]
			_ = store.AddMessage(m.ctx.DB, m.conv.ID, "user", text)
			m.chat = m.chat.appendLocal("You", text)
			if err := s.Delete(context.Background(), m.user.ID, label); err != nil {
				resp := "failed to delete secret: " + err.Error()
				_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
				m.chat = m.chat.appendLocal("System", resp)
				return m, true, nil
			}
			resp := "deleted secret '" + label + "'"
			_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
			m.chat = m.chat.appendLocal("System", resp)
			return m, true, nil

		case "clear":
			_ = store.AddMessage(m.ctx.DB, m.conv.ID, "user", text)
			m.chat = m.chat.appendLocal("You", text)
			if err := s.Clear(context.Background(), m.user.ID); err != nil {
				resp := "failed to clear secrets: " + err.Error()
				_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
				m.chat = m.chat.appendLocal("System", resp)
				return m, true, nil
			}
			resp := "cleared all secrets"
			_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
			m.chat = m.chat.appendLocal("System", resp)
			return m, true, nil
		}

		resp := usageBlock(
			"/secret add <label> <secret>",
			"/secret list",
			"/secret delete <label>",
			"/secret clear",
		)
		_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
		m.chat = m.chat.appendLocal("System", resp)
		return m, true, nil

	case "/subagent":
		if m.conv == nil || m.user == nil {
			return m, true, nil
		}
		if len(fields) < 2 {
			resp := usageBlock(
				"/subagent spawn <prompt>",
				"/subagent status <id>",
			)
			_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
			m.chat = m.chat.appendLocal("System", resp)
			return m, true, nil
		}
		sub := fields[1]
		switch sub {
		case "spawn":
			prompt := strings.TrimSpace(strings.TrimPrefix(text, "/subagent spawn"))
			if prompt == "" {
				resp := "usage: /subagent spawn <prompt>"
				_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
				m.chat = m.chat.appendLocal("System", resp)
				return m, true, nil
			}
			run := m.subMgr.Spawn(m.user.ID, subagents.RunRequest{Prompt: prompt})
			resp := "spawned subagent: " + run.ID + " (status: " + string(run.Status) + ")"
			_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
			m.chat = m.chat.appendLocal("System", resp)
			return m, true, nil

		case "status":
			if len(fields) < 3 {
				resp := "usage: /subagent status <id>"
				_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
				m.chat = m.chat.appendLocal("System", resp)
				return m, true, nil
			}
			id := fields[2]
			run, ok := m.subMgr.GetForUser(m.user.ID, id)
			if !ok {
				resp := "subagent not found: " + id
				_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
				m.chat = m.chat.appendLocal("System", resp)
				return m, true, nil
			}
			resp := "subagent " + run.ID + ": " + string(run.Status)
			if run.Err != "" {
				resp += "\nerror: " + run.Err
			}
			if run.Result != "" {
				resp += "\nresult:\n" + run.Result
			}
			_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
			m.chat = m.chat.appendLocal("System", resp)
			return m, true, nil
		}
		resp := usageBlock(
			"/subagent spawn <prompt>",
			"/subagent status <id>",
		)
		_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
		m.chat = m.chat.appendLocal("System", resp)
		return m, true, nil

	case "/proactive":
		if m.conv == nil || m.user == nil {
			return m, true, nil
		}
		if len(fields) < 3 {
			resp := usageBlock(
				"/proactive action <name>",
				"/proactive agent <id>",
			)
			_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
			m.chat = m.chat.appendLocal("System", resp)
			return m, true, nil
		}
		sub := fields[1]
		switch sub {
		case "action":
			action := fields[2]
			_ = store.AddMessage(m.ctx.DB, m.conv.ID, "user", text)
			m.chat = m.chat.appendLocal("You", text)
			resp := "triggered proactive action: " + action
			_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
			m.chat = m.chat.appendLocal("System", resp)
			return m, true, m.triggerProactiveActionCmd(action, map[string]string{"text": text})

		case "agent":
			agID := fields[2]
			_ = store.AddMessage(m.ctx.DB, m.conv.ID, "user", text)
			m.chat = m.chat.appendLocal("You", text)
			resp := "triggered proactive agent: " + agID
			_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
			m.chat = m.chat.appendLocal("System", resp)
			return m, true, m.triggerProactiveAgentCmd(agID, map[string]string{"text": text})
		}

		resp := usageBlock(
			"/proactive action <name>",
			"/proactive agent <id>",
		)
		_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
		m.chat = m.chat.appendLocal("System", resp)
		return m, true, nil

	case "/logout":
		// No need to record; drop to login screen.
		m.user = nil
		m.conv = nil
		m.view = viewLogin
		m.auth = newAuthModel(authModeLogin).withDisclaimer(m.termProfile().Disclaimer).withSize(m.w, m.h-1)
		m.chat = newChatModel().withTerminalProfile(m.termProfile()).withSize(m.w, m.h-1)
		return m, true, nil
	}

	if m.conv != nil && strings.HasPrefix(fields[0], "/") {
		_ = store.AddMessage(m.ctx.DB, m.conv.ID, "user", text)
		m.chat = m.chat.appendLocal("You", text)
		resp := "unknown command: " + fields[0] + "\nUse /help for commands or $" + strings.TrimPrefix(fields[0], "/") + " to invoke a skill."
		_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
		m.chat = m.chat.appendLocal("System", resp)
		return m, true, nil
	}

	return m, false, nil
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
		ch <- agentReplyMsg{ConversationID: convID, RequestID: requestID, Text: reply.Text, Reasoning: reply.Reasoning, ToolCalls: entries}
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
		ch <- agentReplyMsg{ConversationID: convID, RequestID: requestID, Text: reply.Text, Reasoning: reply.Reasoning, ToolCalls: entries}
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
		return agentReplyMsg{ConversationID: convID, Text: reply.Text, Reasoning: reply.Reasoning, ToolCalls: entries}
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
		return m, m.maybeDispatchWaitlist()
	}
	if targetConvID != 0 && !renderInActiveChat {
		_ = store.AddMessage(m.ctx.DB, targetConvID, "assistant", clean)
	}
	if renderInActiveChat {
		m.chat = m.chat.finishStreamingAssistant(msg.RequestID, clean, msg.Reasoning)
	}
	return m, m.maybeDispatchWaitlist()
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
