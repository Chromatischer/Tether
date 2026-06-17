package tui

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"tether/internal/chatcmd"
	"tether/internal/proactive"
	"tether/internal/store"
)

// commandFunc handles a single slash command. text is the raw input and fields
// is its whitespace-split form (fields[0] is the command itself). It returns
// the updated model and an optional command to run.
type commandFunc func(appModel, string, []string) (appModel, tea.Cmd)

// slashCommands maps a slash command to its handler. handleCommand dispatches
// through this table; unrecognized "/foo" input falls through to the unknown
// command notice.
var slashCommands = map[string]commandFunc{
	"/status":    appModel.cmdStatus,
	"/clear":     appModel.cmdClear,
	"/resume":    appModel.cmdResume,
	"/confirm":   appModel.cmdConfirm,
	"/admin":     appModel.cmdAdmin,
	"/help":      appModel.cmdHelp,
	"/tools":     appModel.cmdTools,
	"/signal":    appModel.cmdSignal,
	"/discord":   appModel.cmdDiscord,
	"/memory":    appModel.cmdMemory,
	"/task":      appModel.cmdTask,
	"/secret":    appModel.cmdSecret,
	"/subagent":  appModel.cmdSubagent,
	"/proactive": appModel.cmdProactive,
	"/logout":    appModel.cmdLogout,
}

// handleCommand dispatches a slash command. The bool reports whether the input
// was handled as a command (false means it should be treated as a chat message).
func (m appModel) handleCommand(text string) (appModel, bool, tea.Cmd) {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return m, true, nil
	}
	if h, ok := slashCommands[fields[0]]; ok {
		nm, cmd := h(m, text, fields)
		return nm, true, cmd
	}
	if m.conv != nil && strings.HasPrefix(fields[0], "/") {
		m = m.echo(text)
		m = m.sys("unknown command: " + fields[0] + "\nUse /help for commands or $" + strings.TrimPrefix(fields[0], "/") + " to invoke a skill.")
		return m, true, nil
	}
	return m, false, nil
}

// echo records the user's command verbatim in the active conversation, both in
// the on-screen transcript and the store. Handlers guard on m.conv before
// reaching here, so the store write is skipped only defensively.
func (m appModel) echo(text string) appModel {
	if m.conv != nil {
		_ = store.AddMessage(m.ctx.DB, m.conv.ID, "user", text)
	}
	m.chat = m.chat.appendLocal("You", text)
	return m
}

// sys records a system/assistant response in the transcript and the store.
func (m appModel) sys(resp string) appModel {
	if m.conv != nil {
		_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
	}
	m.chat = m.chat.appendLocal("System", resp)
	return m
}

func (m appModel) cmdStatus(text string, fields []string) (appModel, tea.Cmd) {
	if m.conv == nil || m.user == nil {
		return m, nil
	}
	m = m.echo(text)
	resp := renderSessionStatusTUI(m.ag.SessionStatus(m.user.ID, m.conv.ID))
	return m.sys(resp), nil
}

func (m appModel) cmdClear(text string, fields []string) (appModel, tea.Cmd) {
	if m.conv == nil || m.user == nil {
		return m, nil
	}
	oldConv := m.conv
	resumeCode := store.EncodeResumeCode(oldConv.ID)
	m = m.echo(text)

	newConv, err := store.CreateConversation(m.ctx.DB, m.user.ID, "")
	if err != nil {
		return m.sys("failed to clear chat: " + err.Error()), nil
	}
	if err := store.SetActiveConversation(m.ctx.DB, m.user.ID, newConv.ID); err != nil {
		return m.sys("failed to switch chat: " + err.Error()), nil
	}
	m.ag.ResetConversationSession(newConv.ID)
	m = m.activateConversation(newConv)
	m = m.sys("Started a fresh conversation with a clean agent context. Resume the previous chat with `/resume " + resumeCode + "`.")
	return m, m.chat.loadCmd()
}

func (m appModel) cmdResume(text string, fields []string) (appModel, tea.Cmd) {
	if m.conv == nil || m.user == nil {
		return m, nil
	}
	if len(fields) != 2 {
		return m.sys("usage: /resume <code>"), nil
	}
	m = m.echo(text)
	convID, err := store.DecodeResumeCode(fields[1])
	if err != nil {
		return m.sys(err.Error()), nil
	}
	conv, ok, err := store.GetConversation(m.ctx.DB, m.user.ID, convID)
	if err != nil {
		return m.sys("failed to resume chat: " + err.Error()), nil
	}
	if !ok {
		return m.sys("conversation not found for that resume code"), nil
	}
	if err := store.SetActiveConversation(m.ctx.DB, m.user.ID, conv.ID); err != nil {
		return m.sys("failed to switch chat: " + err.Error()), nil
	}
	m = m.activateConversation(conv)
	return m, m.chat.loadCmd()
}

func (m appModel) cmdConfirm(text string, fields []string) (appModel, tea.Cmd) {
	if m.conv == nil || m.user == nil {
		return m, nil
	}
	if len(fields) != 2 {
		return m.sys("usage: /confirm <token>"), nil
	}
	m = m.echo(text)
	requestID := m.nextRequestID
	m.nextRequestID++
	m.activeRuns++
	m.releasedRuns[requestID] = false
	m.chat = m.chat.startStreamingAssistant(requestID)
	return m, tea.Batch(m.resumeConfirmationCmd(requestID, fields[1]), m.chat.streamTickCmd())
}

func adminUsageBlock() string {
	return usageBlock(
		"/admin users list",
		"/admin users promote <username>",
		"/admin users demote <username>",
		"/admin audit tail [n]",
		"/admin signal status",
		"/admin jobs status",
	)
}

func (m appModel) cmdAdmin(text string, fields []string) (appModel, tea.Cmd) {
	if m.conv == nil || m.user == nil {
		return m, nil
	}
	if m.user.Role != "admin" {
		return m.sys("admin only"), nil
	}
	if len(fields) < 2 {
		return m.sys(adminUsageBlock()), nil
	}
	switch fields[1] {
	case "users":
		return m.cmdAdminUsers(text, fields), nil
	case "audit":
		return m.cmdAdminAudit(text, fields), nil
	case "signal":
		return m.cmdAdminSignal(text, fields), nil
	case "jobs":
		return m.cmdAdminJobs(text, fields), nil
	}
	return m.sys(adminUsageBlock()), nil
}

func (m appModel) cmdAdminUsers(text string, fields []string) appModel {
	usersUsage := func() string {
		return usageBlock(
			"/admin users list",
			"/admin users promote <username>",
			"/admin users demote <username>",
		)
	}
	if len(fields) < 3 {
		return m.sys(usersUsage())
	}
	sub := fields[2]
	switch sub {
	case "list":
		m = m.echo(text)
		users, err := store.ListUsers(m.ctx.DB)
		if err != nil {
			return m.sys("failed: " + err.Error())
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
		return m.sys(strings.TrimSpace(b.String()))

	case "promote", "demote":
		if len(fields) < 4 {
			return m.sys(usersUsage())
		}
		user := fields[3]
		role := "user"
		if sub == "promote" {
			role = "admin"
		}
		m = m.echo(text)
		if err := store.SetUserRole(m.ctx.DB, user, role); err != nil {
			return m.sys("failed: " + err.Error())
		}
		return m.sys("updated role for " + user + " to " + role)
	}
	return m.sys(usersUsage())
}

func (m appModel) cmdAdminAudit(text string, fields []string) appModel {
	if len(fields) < 3 || fields[2] != "tail" {
		return m.sys("usage: /admin audit tail [n]")
	}
	m = m.echo(text)
	limit := 50
	if len(fields) >= 4 {
		if n, err := strconv.Atoi(fields[3]); err == nil {
			limit = n
		}
	}
	evs, err := store.ListAuditEvents(m.ctx.DB, limit)
	if err != nil {
		return m.sys("failed: " + err.Error())
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
	return m.sys(strings.TrimSpace(b.String()))
}

func (m appModel) cmdAdminSignal(text string, fields []string) appModel {
	m = m.echo(text)
	if len(fields) < 3 || fields[2] != "status" {
		return m.sys("usage: /admin signal status")
	}
	if !m.ctx.Config.Signal.Enabled {
		return m.sys("Signal: disabled")
	}
	addr := strings.TrimSpace(m.ctx.Config.Signal.HTTPAddr)
	base := addr
	if !strings.HasPrefix(base, "http") {
		base = "http://" + base
	}
	req, _ := http.NewRequest(http.MethodGet, base+"/api/v1/check", nil)
	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		return m.sys("Signal: enabled (check failed: " + err.Error() + ")")
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
	return m.sys(resp)
}

func (m appModel) cmdAdminJobs(text string, fields []string) appModel {
	m = m.echo(text)
	if len(fields) < 3 || fields[2] != "status" {
		return m.sys("usage: /admin jobs status")
	}
	lastTick, ok, err := store.LatestAuditEventTime(m.ctx.DB, "proactive_tick")
	if err != nil {
		return m.sys("failed: " + err.Error())
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
	return m.sys(resp)
}

func (m appModel) cmdHelp(text string, fields []string) (appModel, tea.Cmd) {
	if m.conv == nil {
		return m, nil
	}
	m = m.echo(text)
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
	return m.sys(resp), nil
}

func (m appModel) cmdTools(text string, fields []string) (appModel, tea.Cmd) {
	if m.conv == nil {
		return m, nil
	}
	m = m.echo(text)

	if len(fields) > 1 && fields[1] == "describe" {
		if len(fields) < 3 {
			return m.sys("usage: /tools describe <name>"), nil
		}
		return m.sys(chatcmd.ToolsDescribe(m.toolReg, fields[2])), nil
	}

	if len(fields) == 1 || fields[1] == "list" {
		return m.sys(chatcmd.ToolsList(m.toolReg)), nil
	} else if fields[1] == "search" {
		q := ""
		if len(fields) > 2 {
			q = strings.Join(fields[2:], " ")
		}
		return m.sys(chatcmd.ToolsSearch(m.toolReg, q)), nil
	}
	return m.sys(usageBlock(
		"/tools list",
		"/tools search <query>",
		"/tools describe <name>",
	)), nil
}

func (m appModel) cmdSignal(text string, fields []string) (appModel, tea.Cmd) {
	if m.conv == nil || m.user == nil {
		return m, nil
	}
	usage := func() string {
		return usageBlock("/signal link", "/signal status", "/signal unlink")
	}
	if len(fields) < 2 {
		return m.sys(usage()), nil
	}
	switch fields[1] {
	case "link":
		m = m.echo(text)
		return m.sys(chatcmd.SignalLink(m.ctx.DB, m.user.ID, m.ctx.Config.Signal.AccountNumber)), nil

	case "status":
		m = m.echo(text)
		return m.sys(chatcmd.SignalStatus(m.ctx.DB, m.user.ID)), nil

	case "unlink":
		m = m.echo(text)
		return m.sys(chatcmd.SignalUnlink(m.ctx.DB, m.user.ID)), nil
	}
	return m.sys(usage()), nil
}

func (m appModel) cmdDiscord(text string, fields []string) (appModel, tea.Cmd) {
	if m.conv == nil || m.user == nil {
		return m, nil
	}
	usage := func() string {
		return usageBlock("/discord status", "/discord link <code>", "/discord unlink")
	}
	if len(fields) < 2 {
		return m.sys(usage()), nil
	}
	switch fields[1] {
	case "status":
		m = m.echo(text)
		did, ok, err := store.GetDiscordUserID(m.ctx.DB, m.user.ID)
		if err != nil {
			return m.sys("failed to get discord status: " + err.Error()), nil
		}
		if !ok {
			return m.sys("Discord: not linked"), nil
		}
		return m.sys("Discord linked: " + did), nil

	case "unlink":
		m = m.echo(text)
		if err := store.UnlinkDiscordUserID(m.ctx.DB, m.user.ID); err != nil {
			return m.sys("failed to unlink: " + err.Error()), nil
		}
		return m.sys("Discord unlinked"), nil

	case "link":
		if len(fields) < 3 {
			return m.sys("usage: /discord link <code>"), nil
		}
		m = m.echo(text)

		if _, ok, err := store.GetDiscordUserID(m.ctx.DB, m.user.ID); err != nil {
			return m.sys("failed to get discord status: " + err.Error()), nil
		} else if ok {
			return m.sys("Discord already linked. Run /discord unlink first."), nil
		}

		code := strings.TrimSpace(fields[2])
		discordUID, ok, err := store.ConsumeDiscordLinkCode(m.ctx.DB, code)
		if err != nil {
			return m.sys("failed to consume link code: " + err.Error()), nil
		}
		if !ok {
			return m.sys("invalid or expired link code"), nil
		}

		if otherUID, ok2, err := store.FindUserIDByDiscordUserID(m.ctx.DB, discordUID); err != nil {
			return m.sys("failed to check discord link: " + err.Error()), nil
		} else if ok2 {
			if otherUID == m.user.ID {
				return m.sys("Discord already linked."), nil
			}
			return m.sys("That Discord account is already linked to another Tether user."), nil
		}

		if err := store.LinkDiscordUserID(m.ctx.DB, m.user.ID, discordUID); err != nil {
			if err == store.ErrDiscordAlreadyLinkedForUser {
				return m.sys("Discord already linked. Run /discord unlink first."), nil
			}
			return m.sys("failed to link discord: " + err.Error()), nil
		}

		return m.sys("Discord linked."), nil
	}
	return m.sys(usage()), nil
}

func (m appModel) cmdMemory(text string, fields []string) (appModel, tea.Cmd) {
	if m.conv == nil || m.user == nil {
		return m, nil
	}
	usage := func() string {
		return usageBlock(
			"/memory list [kind]",
			"/memory add <kind> <content>",
			"/memory update <id> <content>",
			"/memory delete <id>",
		)
	}
	if len(fields) < 2 {
		return m.sys(usage()), nil
	}
	switch fields[1] {
	case "list":
		m = m.echo(text)
		kind := ""
		if len(fields) >= 3 {
			kind = fields[2]
		}
		return m.sys(chatcmd.MemoryList(m.ctx.DB, m.user.ID, kind)), nil

	case "add":
		if len(fields) < 4 {
			return m.sys("usage: /memory add <kind> <content>"), nil
		}
		m = m.echo(text)
		return m.sys(chatcmd.MemoryAdd(m.ctx.DB, m.user.ID, fields[2], strings.Join(fields[3:], " "))), nil

	case "update":
		if len(fields) < 4 {
			return m.sys("usage: /memory update <id> <content>"), nil
		}
		m = m.echo(text)
		return m.sys(chatcmd.MemoryUpdate(m.ctx.DB, m.user.ID, fields[2], strings.Join(fields[3:], " "))), nil

	case "delete":
		if len(fields) < 3 {
			return m.sys("usage: /memory delete <id>"), nil
		}
		m = m.echo(text)
		return m.sys(chatcmd.MemoryDelete(m.ctx.DB, m.user.ID, fields[2])), nil
	}
	return m.sys(usage()), nil
}

func (m appModel) cmdTask(text string, fields []string) (appModel, tea.Cmd) {
	if m.conv == nil || m.user == nil {
		return m, nil
	}
	usage := func() string {
		return usageBlock(
			"/task list",
			"/task add <text>",
			"/task edit <id> <text>",
			"/task done <id>",
		)
	}
	if len(fields) < 2 {
		return m.sys(usage()), nil
	}
	switch fields[1] {
	case "list":
		m = m.echo(text)
		return m.sys(chatcmd.TaskList(m.ctx.DB, m.user.ID)), nil

	case "add":
		if len(fields) < 3 {
			return m.sys("usage: /task add <text>"), nil
		}
		m = m.echo(text)
		content := strings.Join(fields[2:], " ")
		resp, ok := chatcmd.TaskAdd(m.ctx.DB, m.user.ID, content)
		m = m.sys(resp)
		if !ok {
			return m, nil
		}
		return m, m.triggerProactiveEventCmd(proactive.EventTaskChanged, map[string]string{"text": content})

	case "edit":
		if len(fields) < 4 {
			return m.sys("usage: /task edit <id> <text>"), nil
		}
		m = m.echo(text)
		content := strings.Join(fields[3:], " ")
		resp, ok := chatcmd.TaskUpdate(m.ctx.DB, m.user.ID, fields[2], content)
		m = m.sys(resp)
		if !ok {
			return m, nil
		}
		return m, m.triggerProactiveEventCmd(proactive.EventTaskChanged, map[string]string{"text": content})

	case "done":
		if len(fields) < 3 {
			return m.sys("usage: /task done <id>"), nil
		}
		m = m.echo(text)
		resp, ok := chatcmd.TaskDone(m.ctx.DB, m.user.ID, fields[2])
		m = m.sys(resp)
		if !ok {
			return m, nil
		}
		return m, m.triggerProactiveEventCmd(proactive.EventTaskChanged, map[string]string{"text": text})
	}
	return m.sys(usage()), nil
}

func (m appModel) cmdSecret(text string, fields []string) (appModel, tea.Cmd) {
	if m.conv == nil || m.user == nil {
		return m, nil
	}
	// NOTE: we never persist or display secret plaintext.
	usage := func() string {
		return usageBlock(
			"/secret add <label> <secret>",
			"/secret list",
			"/secret delete <label>",
			"/secret clear",
		)
	}
	if len(fields) < 2 {
		return m.sys(usage()), nil
	}

	s, errMsg := chatcmd.OpenSecretStore(m.ctx.DB, m.ctx.Config.Secrets.MasterKey, m.ctx.Config.Secrets.TTLHours)
	if s == nil {
		return m.sys(errMsg), nil
	}
	ttl := m.ctx.Config.Secrets.TTLHours

	switch fields[1] {
	case "add":
		if len(fields) < 4 {
			return m.sys("usage: /secret add <label> <secret>"), nil
		}
		label := fields[2]
		secretText := strings.Join(fields[3:], " ")
		// Record command without the secret.
		m = m.echo("/secret add " + label + " [REDACTED]")
		return m.sys(chatcmd.SecretAdd(s, m.user.ID, label, secretText, ttl)), nil

	case "list":
		m = m.echo(text)
		return m.sys(chatcmd.SecretList(s, m.user.ID)), nil

	case "delete":
		if len(fields) < 3 {
			return m.sys("usage: /secret delete <label>"), nil
		}
		m = m.echo(text)
		return m.sys(chatcmd.SecretDelete(s, m.user.ID, fields[2])), nil

	case "clear":
		m = m.echo(text)
		return m.sys(chatcmd.SecretClear(s, m.user.ID)), nil
	}
	return m.sys(usage()), nil
}

func (m appModel) cmdSubagent(text string, fields []string) (appModel, tea.Cmd) {
	if m.conv == nil || m.user == nil {
		return m, nil
	}
	usage := func() string {
		return usageBlock(
			"/subagent spawn <prompt>",
			"/subagent status <id>",
		)
	}
	if len(fields) < 2 {
		return m.sys(usage()), nil
	}
	switch fields[1] {
	case "spawn":
		prompt := strings.TrimSpace(strings.TrimPrefix(text, "/subagent spawn"))
		if prompt == "" {
			return m.sys("usage: /subagent spawn <prompt>"), nil
		}
		return m.sys(chatcmd.SubagentSpawn(m.subMgr, m.user.ID, prompt)), nil

	case "status":
		if len(fields) < 3 {
			return m.sys("usage: /subagent status <id>"), nil
		}
		return m.sys(chatcmd.SubagentStatus(m.subMgr, m.user.ID, fields[2])), nil
	}
	return m.sys(usage()), nil
}

func (m appModel) cmdProactive(text string, fields []string) (appModel, tea.Cmd) {
	if m.conv == nil || m.user == nil {
		return m, nil
	}
	usage := func() string {
		return usageBlock(
			"/proactive action <name>",
			"/proactive agent <id>",
		)
	}
	if len(fields) < 3 {
		return m.sys(usage()), nil
	}
	switch fields[1] {
	case "action":
		action := fields[2]
		m = m.echo(text)
		m = m.sys("triggered proactive action: " + action)
		return m, m.triggerProactiveActionCmd(action, map[string]string{"text": text})

	case "agent":
		agID := fields[2]
		m = m.echo(text)
		m = m.sys("triggered proactive agent: " + agID)
		return m, m.triggerProactiveAgentCmd(agID, map[string]string{"text": text})
	}
	return m.sys(usage()), nil
}

func (m appModel) cmdLogout(text string, fields []string) (appModel, tea.Cmd) {
	// No need to record; drop to login screen.
	m.user = nil
	m.conv = nil
	m.view = viewLogin
	m.auth = newAuthModel(authModeLogin).withDisclaimer(m.termProfile().Disclaimer).withSize(m.w, m.h-1)
	m.chat = newChatModel().withTerminalProfile(m.termProfile()).withSize(m.w, m.h-1)
	return m, nil
}
