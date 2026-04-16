package tui

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"charm.land/bubbles/v2/cursor"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"tether/internal/agent"
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
}

func NewAppModel(ctx *SessionContext) tea.Model {
	ag := ctx.Agent
	m := appModel{ctx: ctx, ag: ag, toolReg: tools.DefaultRegistry(), subMgr: ag.Subagents()}
	m.proEng = proactive.NewEngine(ctx.DB, ag, ag.Subagents(), ctx.Config.Paths.DataDir)
	m.view = viewLogin
	m.auth = newAuthModel(authModeLogin)
	m.chat = newChatModel()
	m.memory = newMemoryModel()
	m.settings = newSettingsModel(ctx)
	m.admin = newAdminModel(ctx)
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
		case "ctrl+c", "q":
			return m, tea.Quit
		}

	case tea.MouseClickMsg:
		if msg.Button == tea.MouseLeft && msg.Y == 0 {
			if b, ok := m.hitHeader(msg.X); ok {
				switch b.ID {
				case "login":
					m.view = viewLogin
					m.auth = newAuthModel(authModeLogin).withSize(m.w, m.h-1)
				case "signup":
					m.view = viewSignup
					m.auth = newAuthModel(authModeSignup).withSize(m.w, m.h-1)
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
		conv, err := store.GetOrCreateDefaultConversation(m.ctx.DB, u.ID)
		if err != nil {
			m.auth, _ = m.auth.Update(authStatusMsg{Text: err.Error(), IsErr: true})
			return m, nil
		}

		// Deliver any pending proactive notifications into the conversation.
		nots, err := store.ListUndeliveredNotifications(m.ctx.DB, u.ID, 50)
		if err == nil {
			for _, n := range nots {
				msg := "[Proactive/" + n.Kind + "] " + n.Content
				_ = store.AddMessage(m.ctx.DB, conv.ID, "assistant", msg)
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

		m.user = u
		m.conv = conv
		m.view = viewChat
		m.chat = m.chat.withConversation(m.ctx.DB, m.user.ID, m.conv.ID)
		m.chat = m.chat.withSize(m.w, m.h-1)
		m.memory = m.memory.withUser(m.ctx.DB, m.user.ID).withSize(m.w, m.h-1)
		m.settings = m.settings.withUser(m.user.ID).withSize(m.w, m.h-1)
		return m, tea.Batch(
			m.chat.loadCmd(),
			m.triggerProactiveEventCmd(proactive.EventLogin, nil),
		)

	case loginSuccessMsg:
		m.user = msg.User
		m.conv = msg.Conv
		m.view = viewChat
		m.chat = m.chat.withConversation(m.ctx.DB, m.user.ID, m.conv.ID)
		m.chat = m.chat.withSize(m.w, m.h-1)
		m.memory = m.memory.withUser(m.ctx.DB, m.user.ID).withSize(m.w, m.h-1)
		m.settings = m.settings.withUser(m.user.ID).withSize(m.w, m.h-1)
		return m, tea.Batch(
			m.chat.loadCmd(),
			m.triggerProactiveEventCmd(proactive.EventLogin, nil),
		)

	case chatSendMsg:
		if strings.HasPrefix(msg.Text, "/") {
			m2, handled, cmd := m.handleCommand(msg.Text)
			if handled {
				return m2, cmd
			}
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
		return m, tea.Batch(
			m.askAgentCmd(clean),
			m.triggerProactiveEventCmd(proactive.EventUserMessage, map[string]string{"text": clean}),
		)

	case agentReplyMsg:
		clean, findings := redact.ScanAndRedact(msg.Text)
		if len(findings) > 0 {
			warn := "The assistant response contained secret-like content and was redacted."
			_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", warn)
			m.chat = m.chat.appendLocal("System", warn)
		}
		_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", clean)
		m.chat = m.chat.appendLocal("Tether", clean)
		return m, nil

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
	if m.view == viewChat {
		v.Cursor = m.chat.cursor()
	}
	if m.view == viewLogin || m.view == viewSignup {
		v.Cursor = m.auth.cursor()
	}
	return v
}

func (m appModel) renderHeader() string {
	if m.w <= 0 {
		return ""
	}

	brand := styleHeaderBrand.Render("tether")

	buttons := m.headerButtons()
	tabParts := make([]string, 0, len(buttons))
	for _, b := range buttons {
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
		if active {
			tabParts = append(tabParts, styleTabActive.Render(b.Label))
		} else {
			tabParts = append(tabParts, styleTab.Render(b.Label))
		}
	}
	tabs := lipgloss.JoinHorizontal(lipgloss.Top, tabParts...)

	gap := m.w - lipgloss.Width(brand) - lipgloss.Width(tabs)
	if gap < 0 {
		gap = 0
	}
	spacer := styleHeaderSpacer.Render(strings.Repeat(" ", gap))

	return brand + spacer + tabs
}

func (m appModel) headerButtons() []headerButton {
	// Keep this in sync with renderHeader.
	if m.user == nil {
		return []headerButton{
			{ID: "login", Label: "Login"},
			{ID: "signup", Label: "Sign up"},
		}
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
	// Tabs are right-aligned; compute their starting x offset.
	btns := m.headerButtons()
	widths := make([]int, len(btns))
	totalW := 0
	for i, b := range btns {
		w := lipgloss.Width(styleTab.Render(b.Label))
		widths[i] = w
		totalW += w
	}
	startX := m.w - totalW
	curX := startX
	for i := range btns {
		btns[i].X0 = curX
		btns[i].X1 = curX + widths[i]
		curX += widths[i]
	}
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
		ok := m.ag.ConfirmToken(m.user.ID, fields[1])
		resp := "confirmation failed"
		if ok {
			resp = "confirmed"
		}
		_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
		m.chat = m.chat.appendLocal("System", resp)
		return m, true, nil

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
			resp := "usage: /admin users ... | /admin audit tail [n] | /admin signal status | /admin jobs status"
			_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
			m.chat = m.chat.appendLocal("System", resp)
			return m, true, nil
		}
		section := fields[1]
		switch section {
		case "users":
			if len(fields) < 3 {
				resp := "usage: /admin users list | /admin users promote <username> | /admin users demote <username>"
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
					resp := "usage: /admin users promote <username> | /admin users demote <username>"
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
			resp := "usage: /admin users list | /admin users promote <username> | /admin users demote <username>"
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

		resp := "usage: /admin users ... | /admin audit tail [n] | /admin signal status | /admin jobs status"
		_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
		m.chat = m.chat.appendLocal("System", resp)
		return m, true, nil

	case "/help":
		if m.conv != nil {
			_ = store.AddMessage(m.ctx.DB, m.conv.ID, "user", text)
			m.chat = m.chat.appendLocal("You", text)
			resp := "Commands:\n" +
				"  /help\n" +
				"  /logout\n" +
				"  /tools list\n" +
				"  /tools search <query>\n" +
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
				"  /admin users list (admin)\n" +
				"  /admin audit tail [n] (admin)\n" +
				"  /admin signal status (admin)\n" +
				"  /admin jobs status (admin)\n" +
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
			resp := "usage: /tools list | /tools search <query>"
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
			resp := "usage: /signal link | /signal status | /signal unlink"
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

		resp := "usage: /signal link | /signal status | /signal unlink"
		_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
		m.chat = m.chat.appendLocal("System", resp)
		return m, true, nil

	case "/discord":
		if m.conv == nil || m.user == nil {
			return m, true, nil
		}
		if len(fields) < 2 {
			resp := "usage: /discord status | /discord link <code> | /discord unlink"
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

		resp := "usage: /discord status | /discord link <code> | /discord unlink"
		_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
		m.chat = m.chat.appendLocal("System", resp)
		return m, true, nil

	case "/memory":
		if m.conv == nil || m.user == nil {
			return m, true, nil
		}
		if len(fields) < 2 {
			resp := "usage: /memory list [kind] | /memory add <kind> <content> | /memory update <id> <content> | /memory delete <id>"
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

		resp := "usage: /memory list [kind] | /memory add <kind> <content> | /memory update <id> <content> | /memory delete <id>"
		_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
		m.chat = m.chat.appendLocal("System", resp)
		return m, true, nil

	case "/task":
		if m.conv == nil || m.user == nil {
			return m, true, nil
		}
		if len(fields) < 2 {
			resp := "usage: /task list | /task add <text> | /task edit <id> <text> | /task done <id>"
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

		resp := "usage: /task list | /task add <text> | /task edit <id> <text> | /task done <id>"
		_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
		m.chat = m.chat.appendLocal("System", resp)
		return m, true, nil

	case "/secret":
		if m.conv == nil || m.user == nil {
			return m, true, nil
		}
		// NOTE: we never persist or display secret plaintext.
		if len(fields) < 2 {
			resp := "usage: /secret add <label> <secret> | /secret list | /secret delete <label> | /secret clear"
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

		resp := "usage: /secret add <label> <secret> | /secret list | /secret delete <label> | /secret clear"
		_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
		m.chat = m.chat.appendLocal("System", resp)
		return m, true, nil

	case "/subagent":
		if m.conv == nil || m.user == nil {
			return m, true, nil
		}
		if len(fields) < 2 {
			resp := "usage: /subagent spawn <prompt> | /subagent status <id>"
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
			run := m.subMgr.Spawn(m.user.ID, prompt)
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
			run, ok := m.subMgr.Get(id)
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
		resp := "usage: /subagent spawn <prompt> | /subagent status <id>"
		_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
		m.chat = m.chat.appendLocal("System", resp)
		return m, true, nil

	case "/proactive":
		if m.conv == nil || m.user == nil {
			return m, true, nil
		}
		if len(fields) < 3 {
			resp := "usage: /proactive action <name> | /proactive agent <id>"
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

		resp := "usage: /proactive action <name> | /proactive agent <id>"
		_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
		m.chat = m.chat.appendLocal("System", resp)
		return m, true, nil

	case "/logout":
		// No need to record; drop to login screen.
		m.user = nil
		m.conv = nil
		m.view = viewLogin
		m.auth = newAuthModel(authModeLogin).withSize(m.w, m.h-1)
		m.chat = newChatModel().withSize(m.w, m.h-1)
		return m, true, nil
	}

	return m, false, nil
}

func (m appModel) askAgentCmd(text string) tea.Cmd {
	userID := m.user.ID
	convID := m.conv.ID
	ag := m.ag
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		reply, err := ag.Reply(ctx, agent.ReplyParams{UserID: userID, ConversationID: convID, Text: text})
		if err != nil {
			return agentReplyMsg{Text: "(agent error) " + err.Error()}
		}
		return agentReplyMsg{Text: reply.Text}
	}
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
