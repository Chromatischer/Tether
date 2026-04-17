package discord

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"charm.land/log/v2"
	"github.com/bwmarrin/discordgo"

	"tether/internal/agent"
	"tether/internal/config"
	"tether/internal/proactive"
	"tether/internal/redact"
	"tether/internal/secrets"
	"tether/internal/store"
	"tether/internal/tools"
)

// Gateway integrates Discord DMs via a bot token.
//
// Behavior (MVP):
// - Only reacts to direct DMs (not guild messages, not group DMs)
// - If sender isn't linked, replies with "Your account is not linked" + a link code
// - If linked, forwards to the agent and replies with the agent output
type Gateway struct {
	cfg *config.Config
	db  *sql.DB
	ag  *agent.Agent

	s *discordgo.Session
}

const (
	discordMessageLimit         = 1900
	discordEditInterval         = 1200 * time.Millisecond
	discordTypingInterval       = 8 * time.Second
	discordStreamingActivity    = "Answering DMs"
	discordStreamingPlaceholder = "..."
)

func NewGateway(cfg *config.Config, db *sql.DB, ag *agent.Agent) *Gateway {
	return &Gateway{cfg: cfg, db: db, ag: ag}
}

func (g *Gateway) Start(ctx context.Context) {
	if !g.cfg.Discord.Enabled {
		log.Info("discord gateway disabled")
		return
	}
	if strings.TrimSpace(g.cfg.Discord.BotToken) == "" {
		log.Warn("discord enabled but bot_token missing")
		return
	}

	s, err := discordgo.New("Bot " + strings.TrimSpace(g.cfg.Discord.BotToken))
	if err != nil {
		log.Error("failed to create discord session", "error", err)
		return
	}
	g.s = s

	// Gateway intents:
	// - DirectMessages: receive DM events
	// - MessageContent: required to receive message content (privileged; enable in Discord developer portal)
	// - Guilds: not strictly required for DM handling, but helps Discord associate the session with the bot's guilds
	//   so presence/activity reliably shows as online in servers.
	s.Identify.Intents = discordgo.IntentsGuilds | discordgo.IntentsDirectMessages | discordgo.IntentsMessageContent
	// Set a baseline presence in the Identify payload.
	s.Identify.Presence = discordgo.GatewayStatusUpdate{Status: "online"}

	// Connection lifecycle diagnostics.
	s.AddHandler(func(_ *discordgo.Session, _ *discordgo.Connect) {
		log.Info("discord connected")
	})
	s.AddHandler(func(_ *discordgo.Session, _ *discordgo.Disconnect) {
		log.Warn("discord disconnected")
	})
	s.AddHandler(func(s *discordgo.Session, r *discordgo.Ready) {
		if r != nil && r.User != nil {
			log.Info("discord ready", "user", r.User.Username, "id", r.User.ID, "guilds", len(r.Guilds))
		} else {
			log.Info("discord ready")
		}
		// Re-apply activity on READY (and after reconnects).
		if err := s.UpdateGameStatus(0, discordStreamingActivity); err != nil {
			log.Warn("failed to update discord presence", "error", err)
		}
	})
	s.AddHandler(func(s *discordgo.Session, _ *discordgo.Resumed) {
		log.Info("discord resumed")
		// Presence can get reset during reconnect/resume; best-effort re-apply.
		if err := s.UpdateGameStatus(0, discordStreamingActivity); err != nil {
			log.Warn("failed to update discord presence", "error", err)
		}
	})

	s.AddHandler(func(s *discordgo.Session, m *discordgo.MessageCreate) {
		// Don't block the discordgo event loop.
		go g.onMessage(ctx, s, m)
	})

	if err := s.Open(); err != nil {
		log.Error("failed to open discord session", "error", err)
		return
	}
	log.Info("discord gateway started")

	go func() {
		<-ctx.Done()
		_ = s.Close()
	}()

	go g.pruneLoop(ctx)
}

func (g *Gateway) pruneLoop(ctx context.Context) {
	t := time.NewTicker(30 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			store.PruneDiscordLinkCodes(g.db)
		}
	}
}

func (g *Gateway) onMessage(ctx context.Context, s *discordgo.Session, m *discordgo.MessageCreate) {
	if m == nil || m.Message == nil || m.Author == nil {
		return
	}
	if m.Author.Bot {
		return
	}

	// Ignore non-DM messages.
	if m.GuildID != "" {
		return
	}
	ch, err := s.State.Channel(m.ChannelID)
	if err != nil || ch == nil {
		ch, _ = s.Channel(m.ChannelID)
	}
	if ch == nil {
		return
	}
	if ch.Type != discordgo.ChannelTypeDM {
		// Ignore group DMs and other channel types.
		return
	}

	content := strings.TrimSpace(m.Content)
	if content == "" {
		return
	}

	discordUID := strings.TrimSpace(m.Author.ID)
	if discordUID == "" {
		return
	}

	uid, linked, err := store.FindUserIDByDiscordUserID(g.db, discordUID)
	if err != nil {
		return
	}

	if !linked {
		code, err := store.CreateDiscordLinkCode(g.db, discordUID, 10*time.Minute)
		if err != nil {
			return
		}
		msg := "Your account is not linked\n" +
			"Link code: " + code + "\n" +
			"In Tether SSH run: /discord link " + code + "\n" +
			"(Expires in ~10 minutes.)"
		_, _ = s.ChannelMessageSend(m.ChannelID, msg)
		return
	}

	conv, err := store.GetOrCreateActiveConversation(g.db, uid)
	if err != nil {
		_, _ = s.ChannelMessageSend(m.ChannelID, "Internal error.")
		return
	}

	if handled, resp, nextConv := g.handleCommand(ctx, uid, conv, content); handled {
		if nextConv != nil {
			conv = nextConv
		}
		if strings.TrimSpace(resp) != "" {
			_ = store.AddMessage(g.db, conv.ID, "assistant", resp)
			sendDiscordChunks(s, m.ChannelID, resp)
		}
		return
	}

	// Best-effort audit without storing message content.
	sum := sha256.Sum256([]byte(content))
	payload, _ := json.Marshal(map[string]any{"from": discordUID, "len": len(content), "sha256": hex.EncodeToString(sum[:])})
	_ = store.AddAuditEvent(g.db, &uid, "discord_inbound", string(payload))

	clean, findings := redact.ScanAndRedact(content)
	_ = store.AddMessage(g.db, conv.ID, "user", clean)
	if len(findings) > 0 {
		_, _ = s.ChannelMessageSend(m.ChannelID, "Your message looked like it contained secrets/tokens and was redacted. Please use /secret add via SSH for secrets.")
	}

	stream := newDiscordReplyStream(ctx, s, m.ChannelID)
	defer stream.Close()
	stream.Start()

	reply, err := g.ag.ReplyStream(ctx, agent.ReplyParams{UserID: uid, ConversationID: conv.ID, Text: clean}, func(ev agent.StreamEvent) {
		stream.OnEvent(ev)
	})
	if err != nil {
		stream.Finish("Agent error: " + err.Error())
		return
	}

	out, of := redact.ScanAndRedact(reply.Text)
	_ = store.AddMessage(g.db, conv.ID, "assistant", out)

	// Best-effort audit without storing message content.
	sum2 := sha256.Sum256([]byte(out))
	payload2, _ := json.Marshal(map[string]any{"to": discordUID, "len": len(out), "sha256": hex.EncodeToString(sum2[:])})
	_ = store.AddAuditEvent(g.db, &uid, "discord_send", string(payload2))

	if len(of) > 0 {
		stream.Finish("(Assistant response was redacted due to secret-like content.)\n" + out)
		return
	}
	stream.Finish(out)
}

func (g *Gateway) sendChunks(s *discordgo.Session, channelID string, msg string) {
	sendDiscordChunks(s, channelID, msg)
}

type discordReplyStream struct {
	ctx     context.Context
	cancel  context.CancelFunc
	s       *discordgo.Session
	channel string
	ticker  *time.Ticker

	mu        sync.Mutex
	messageID string
	lastSent  string
	lastEdit  time.Time
	dirty     bool

	reasoning strings.Builder
	answer    strings.Builder
	tools     []string
	finalText string
}

func newDiscordReplyStream(parent context.Context, s *discordgo.Session, channelID string) *discordReplyStream {
	ctx, cancel := context.WithCancel(parent)
	return &discordReplyStream{
		ctx:     ctx,
		cancel:  cancel,
		s:       s,
		channel: channelID,
		ticker:  time.NewTicker(discordEditInterval),
	}
}

func (r *discordReplyStream) Start() {
	r.ensureMessage(discordStreamingPlaceholder)
	go r.flushLoop()
	go r.typingLoop()
}

func (r *discordReplyStream) Close() {
	r.cancel()
	if r.ticker != nil {
		r.ticker.Stop()
	}
}

func (r *discordReplyStream) OnEvent(ev agent.StreamEvent) {
	r.mu.Lock()
	switch ev.Type {
	case "reasoning_delta":
		r.reasoning.WriteString(ev.Delta)
		r.dirty = true
	case "assistant_delta":
		r.answer.WriteString(ev.Delta)
		r.dirty = true
	case "tool_call":
		if name := strings.TrimSpace(ev.Tool.Name); name != "" {
			r.tools = append(r.tools, name)
			r.dirty = true
		}
	}
	r.mu.Unlock()
	r.flush(false)
}

func (r *discordReplyStream) Finish(text string) {
	r.mu.Lock()
	r.finalText = strings.TrimSpace(text)
	if r.finalText == "" {
		r.finalText = "(No response.)"
	}
	r.dirty = false
	r.mu.Unlock()

	r.cancel()
	if r.ticker != nil {
		r.ticker.Stop()
	}
	r.flushFinal()
}

func (r *discordReplyStream) flushLoop() {
	for {
		select {
		case <-r.ctx.Done():
			return
		case <-r.ticker.C:
			r.flush(false)
		}
	}
}

func (r *discordReplyStream) typingLoop() {
	_ = r.s.ChannelTyping(r.channel)
	t := time.NewTicker(discordTypingInterval)
	defer t.Stop()
	for {
		select {
		case <-r.ctx.Done():
			return
		case <-t.C:
			_ = r.s.ChannelTyping(r.channel)
		}
	}
}

func (r *discordReplyStream) flush(force bool) {
	r.mu.Lock()
	if !r.dirty && !force {
		r.mu.Unlock()
		return
	}
	if !force && !r.lastEdit.IsZero() && time.Since(r.lastEdit) < discordEditInterval {
		r.mu.Unlock()
		return
	}
	content := renderDiscordPreview(r.tools, r.reasoning.String(), r.answer.String())
	messageID := r.messageID
	channelID := r.channel
	lastSent := r.lastSent
	r.mu.Unlock()

	if content == "" {
		content = discordStreamingPlaceholder
	}
	content = truncateDiscordPreview(content, discordMessageLimit)
	if content == lastSent {
		r.mu.Lock()
		r.dirty = false
		r.mu.Unlock()
		return
	}

	if messageID == "" {
		msg, err := r.s.ChannelMessageSend(channelID, content)
		if err != nil {
			log.Warn("failed to send discord streaming message", "error", err)
			return
		}
		r.mu.Lock()
		r.messageID = msg.ID
		r.lastSent = content
		r.lastEdit = time.Now()
		r.dirty = false
		r.mu.Unlock()
		return
	}

	edit := discordgo.NewMessageEdit(channelID, messageID)
	edit.SetContent(content)
	if _, err := r.s.ChannelMessageEditComplex(edit); err != nil {
		log.Warn("failed to edit discord streaming message", "error", err)
		return
	}
	r.mu.Lock()
	r.lastSent = content
	r.lastEdit = time.Now()
	r.dirty = false
	r.mu.Unlock()
}

func (r *discordReplyStream) flushFinal() {
	r.mu.Lock()
	content := r.finalText
	channelID := r.channel
	messageID := r.messageID
	r.mu.Unlock()

	chunks := splitDiscordMessage(content, discordMessageLimit)
	if len(chunks) == 0 {
		chunks = []string{"(No response.)"}
	}
	if messageID == "" {
		sendDiscordChunks(r.s, channelID, strings.Join(chunks, "\n"))
		return
	}
	edit := discordgo.NewMessageEdit(channelID, messageID)
	edit.SetContent(chunks[0])
	if _, err := r.s.ChannelMessageEditComplex(edit); err != nil {
		log.Warn("failed to edit discord final message", "error", err)
		_, _ = r.s.ChannelMessageSend(channelID, chunks[0])
	} else {
		r.mu.Lock()
		r.lastSent = chunks[0]
		r.lastEdit = time.Now()
		r.mu.Unlock()
	}
	for _, chunk := range chunks[1:] {
		_, _ = r.s.ChannelMessageSend(channelID, chunk)
	}
}

func (r *discordReplyStream) ensureMessage(content string) {
	content = strings.TrimSpace(content)
	if content == "" {
		content = discordStreamingPlaceholder
	}
	msg, err := r.s.ChannelMessageSend(r.channel, content)
	if err != nil {
		log.Warn("failed to send discord placeholder message", "error", err)
		return
	}
	r.mu.Lock()
	r.messageID = msg.ID
	r.lastSent = content
	r.lastEdit = time.Now()
	r.mu.Unlock()
}

func renderDiscordPreview(tools []string, reasoning string, answer string) string {
	parts := make([]string, 0, len(tools)+2)
	for _, name := range tools {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		parts = append(parts, "tool: "+name)
	}

	answer = strings.TrimSpace(answer)
	if answer != "" {
		parts = append(parts, answer)
		return strings.TrimSpace(strings.Join(parts, "\n"))
	}

	reasoning = strings.TrimSpace(reasoning)
	if reasoning != "" {
		if len(parts) > 0 {
			parts = append(parts, "")
		}
		parts = append(parts, reasoning)
	}
	if len(parts) == 0 {
		return discordStreamingPlaceholder
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

func truncateDiscordPreview(msg string, max int) string {
	msg = strings.TrimSpace(msg)
	if max <= 0 || len(msg) <= max {
		return msg
	}
	suffix := "\n…"
	if max <= len(suffix) {
		return msg[:max]
	}
	head := msg[:max-len(suffix)]
	if i := strings.LastIndex(head, "\n"); i > 400 {
		head = head[:i]
	}
	return strings.TrimRight(head, "\n") + suffix
}

func splitDiscordMessage(msg string, max int) []string {
	msg = strings.TrimSpace(msg)
	if msg == "" {
		return nil
	}
	if max <= 0 {
		max = discordMessageLimit
	}
	chunks := make([]string, 0, len(msg)/max+1)
	for len(msg) > max {
		chunk := msg[:max]
		// Try not to split in the middle of a line.
		if i := strings.LastIndex(chunk, "\n"); i > 400 {
			chunk = chunk[:i]
		}
		chunks = append(chunks, strings.TrimRight(chunk, "\n"))
		msg = strings.TrimLeft(msg[len(chunk):], "\n")
	}
	if msg != "" {
		chunks = append(chunks, msg)
	}
	return chunks
}

func sendDiscordChunks(s *discordgo.Session, channelID string, msg string) {
	for _, chunk := range splitDiscordMessage(msg, discordMessageLimit) {
		_, _ = s.ChannelMessageSend(channelID, chunk)
	}
}

func (g *Gateway) handleCommand(ctx context.Context, userID int64, conv *store.Conversation, text string) (handled bool, resp string, nextConv *store.Conversation) {
	fields := strings.Fields(text)
	if len(fields) == 0 || !strings.HasPrefix(fields[0], "/") {
		return false, "", conv
	}
	if conv == nil {
		return true, "", conv
	}

	recordUser := func(content string) {
		if strings.TrimSpace(content) != "" {
			_ = store.AddMessage(g.db, conv.ID, "user", content)
		}
	}
	triggerProactiveEvent := func(event string, meta map[string]string) {
		eng := proactive.NewEngine(g.db, g.ag, g.ag.Subagents(), g.cfg.Paths.DataDir)
		go func() {
			ctx2, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			eng.TriggerEvent(ctx2, userID, event, meta)
		}()
	}
	triggerProactiveAction := func(action string, meta map[string]string) {
		eng := proactive.NewEngine(g.db, g.ag, g.ag.Subagents(), g.cfg.Paths.DataDir)
		go func() {
			ctx2, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			eng.TriggerAction(ctx2, userID, action, meta)
		}()
	}
	triggerProactiveAgent := func(agentID string, meta map[string]string) {
		eng := proactive.NewEngine(g.db, g.ag, g.ag.Subagents(), g.cfg.Paths.DataDir)
		go func() {
			ctx2, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			eng.TriggerAgent(ctx2, userID, agentID, meta)
		}()
	}

	switch fields[0] {
	case "/help":
		recordUser(text)
		return true, strings.TrimSpace(
			"Commands:\n" +
				"  /help\n" +
				"  /clear\n" +
				"  /resume <code>\n" +
				"  /confirm <token>\n" +
				"  /tools list\n" +
				"  /tools search <query>\n" +
				"  /tools describe <name>\n" +
				"  /signal status\n" +
				"  /discord status\n" +
				"  /discord unlink\n" +
				"  /memory list [kind]\n" +
				"  /memory add <kind> <content>\n" +
				"  /memory update <id> <content>\n" +
				"  /memory delete <id>\n" +
				"  /task list\n" +
				"  /task add <text>\n" +
				"  /task edit <id> <text>\n" +
				"  /task done <id>\n" +
				"  /secret add <label> <secret>\n" +
				"  /secret list\n" +
				"  /secret delete <label>\n" +
				"  /secret clear\n" +
				"  /subagent spawn <prompt>\n" +
				"  /subagent status <id>\n" +
				"  /proactive action <name>\n" +
				"  /proactive agent <id>",
		), conv

	case "/clear":
		recordUser(text)
		newConv, err := store.CreateConversation(g.db, userID, "")
		if err != nil {
			return true, "failed to clear chat: " + err.Error(), conv
		}
		if err := store.SetActiveConversation(g.db, userID, newConv.ID); err != nil {
			return true, "failed to switch chat: " + err.Error(), conv
		}
		g.ag.ResetConversationSession(newConv.ID)
		return true, "Started a fresh conversation with a clean agent context. Resume the previous chat with `/resume " + store.EncodeResumeCode(conv.ID) + "`.", newConv

	case "/resume":
		if len(fields) != 2 {
			return true, "usage: /resume <code>", conv
		}
		recordUser(text)
		convID, err := store.DecodeResumeCode(fields[1])
		if err != nil {
			return true, err.Error(), conv
		}
		next, ok, err := store.GetConversation(g.db, userID, convID)
		if err != nil {
			return true, "failed to resume chat: " + err.Error(), conv
		}
		if !ok {
			return true, "conversation not found for that resume code", conv
		}
		if err := store.SetActiveConversation(g.db, userID, next.ID); err != nil {
			return true, "failed to switch chat: " + err.Error(), conv
		}
		return true, "Resumed conversation " + store.EncodeResumeCode(next.ID) + ".", next

	case "/confirm":
		if len(fields) != 2 {
			return true, "usage: /confirm <token>", conv
		}
		recordUser(text)
		if g.ag.ConfirmToken(userID, fields[1]) {
			return true, "confirmed", conv
		}
		return true, "confirmation failed", conv

	case "/tools":
		recordUser(text)
		reg := g.ag.ToolRegistry()
		if len(fields) > 1 && fields[1] == "describe" {
			if len(fields) < 3 {
				return true, "usage: /tools describe <name>", conv
			}
			spec, ok := reg.Get(strings.TrimSpace(fields[2]))
			if !ok {
				return true, "unknown tool: " + strings.TrimSpace(fields[2]), conv
			}
			return true, strings.TrimSpace(tools.RenderToolMarkdown(spec)), conv
		}
		var infos []tools.ToolInfo
		if len(fields) == 1 || fields[1] == "list" {
			infos = reg.List()
		} else if fields[1] == "search" {
			infos = reg.Search(strings.Join(fields[2:], " "))
		} else {
			return true, "usage: /tools list | /tools search <query> | /tools describe <name>", conv
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
		return true, strings.TrimSpace(b.String()), conv

	case "/signal":
		if len(fields) < 2 {
			return true, "usage: /signal link | /signal status | /signal unlink", conv
		}
		switch fields[1] {
		case "link":
			recordUser(text)
			code, err := store.CreateSignalLinkCode(g.db, userID, 10*time.Minute)
			if err != nil {
				return true, "failed to create link code: " + err.Error(), conv
			}
			acct := strings.TrimSpace(g.cfg.Signal.AccountNumber)
			if acct == "" {
				acct = "<signal account not configured>"
			}
			return true, "Signal link code: " + code + "\nSend this code from your phone number to the Tether Signal account: " + acct + "\n(Code expires in ~10 minutes.)", conv
		case "status":
			recordUser(text)
			n, ok, err := store.GetSignalNumber(g.db, userID)
			if err != nil {
				return true, "failed to get signal status: " + err.Error(), conv
			}
			if !ok {
				return true, "Signal: not linked", conv
			}
			return true, "Signal linked: " + n, conv
		case "unlink":
			recordUser(text)
			if err := store.UnlinkSignalNumber(g.db, userID); err != nil {
				return true, "failed to unlink: " + err.Error(), conv
			}
			return true, "Signal unlinked", conv
		default:
			return true, "usage: /signal link | /signal status | /signal unlink", conv
		}

	case "/discord":
		if len(fields) < 2 {
			return true, "usage: /discord status | /discord unlink", conv
		}
		switch fields[1] {
		case "status":
			recordUser(text)
			did, ok, err := store.GetDiscordUserID(g.db, userID)
			if err != nil {
				return true, "failed to get discord status: " + err.Error(), conv
			}
			if !ok {
				return true, "Discord: not linked", conv
			}
			return true, "Discord linked: " + did, conv
		case "unlink":
			recordUser(text)
			if err := store.UnlinkDiscordUserID(g.db, userID); err != nil {
				return true, "failed to unlink: " + err.Error(), conv
			}
			return true, "Discord unlinked", conv
		default:
			return true, "usage: /discord status | /discord unlink", conv
		}

	case "/memory":
		if len(fields) < 2 {
			return true, "usage: /memory list [kind] | /memory add <kind> <content> | /memory update <id> <content> | /memory delete <id>", conv
		}
		switch fields[1] {
		case "list":
			recordUser(text)
			kind := ""
			if len(fields) >= 3 {
				kind = fields[2]
			}
			items, err := store.ListMemoryItems(g.db, userID, kind, 100)
			if err != nil {
				return true, "failed to list memory: " + err.Error(), conv
			}
			if len(items) == 0 {
				return true, "no memory items", conv
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
			return true, strings.TrimSpace(b.String()), conv
		case "add":
			if len(fields) < 4 {
				return true, "usage: /memory add <kind> <content>", conv
			}
			recordUser(text)
			id, err := store.AddMemoryItem(g.db, userID, fields[2], strings.Join(fields[3:], " "))
			if err != nil {
				return true, "failed to add memory: " + err.Error(), conv
			}
			return true, "memory added (id " + fmt.Sprintf("%d", id) + ")", conv
		case "update":
			if len(fields) < 4 {
				return true, "usage: /memory update <id> <content>", conv
			}
			recordUser(text)
			id, err := strconv.ParseInt(fields[2], 10, 64)
			if err != nil {
				return true, "invalid id", conv
			}
			if err := store.UpdateMemoryItem(g.db, userID, id, strings.Join(fields[3:], " ")); err != nil {
				return true, "failed to update memory: " + err.Error(), conv
			}
			return true, "memory updated", conv
		case "delete":
			if len(fields) < 3 {
				return true, "usage: /memory delete <id>", conv
			}
			recordUser(text)
			id, err := strconv.ParseInt(fields[2], 10, 64)
			if err != nil {
				return true, "invalid id", conv
			}
			if err := store.DeleteMemoryItem(g.db, userID, id); err != nil {
				return true, "failed to delete memory: " + err.Error(), conv
			}
			return true, "memory deleted", conv
		default:
			return true, "usage: /memory list [kind] | /memory add <kind> <content> | /memory update <id> <content> | /memory delete <id>", conv
		}

	case "/task":
		if len(fields) < 2 {
			return true, "usage: /task list | /task add <text> | /task edit <id> <text> | /task done <id>", conv
		}
		switch fields[1] {
		case "list":
			recordUser(text)
			items, err := store.ListMemoryItems(g.db, userID, "task", 100)
			if err != nil {
				return true, "failed: " + err.Error(), conv
			}
			if len(items) == 0 {
				return true, "no tasks", conv
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
			return true, strings.TrimSpace(b.String()), conv
		case "add":
			if len(fields) < 3 {
				return true, "usage: /task add <text>", conv
			}
			recordUser(text)
			content := strings.Join(fields[2:], " ")
			id, err := store.AddMemoryItem(g.db, userID, "task", content)
			if err != nil {
				return true, "failed: " + err.Error(), conv
			}
			triggerProactiveEvent(proactive.EventTaskChanged, map[string]string{"text": content})
			return true, "task added (id " + fmt.Sprintf("%d", id) + ")", conv
		case "edit":
			if len(fields) < 4 {
				return true, "usage: /task edit <id> <text>", conv
			}
			recordUser(text)
			id, err := strconv.ParseInt(fields[2], 10, 64)
			if err != nil {
				return true, "invalid id", conv
			}
			content := strings.Join(fields[3:], " ")
			if err := store.UpdateMemoryItem(g.db, userID, id, content); err != nil {
				return true, "failed: " + err.Error(), conv
			}
			triggerProactiveEvent(proactive.EventTaskChanged, map[string]string{"text": content})
			return true, "task updated", conv
		case "done":
			if len(fields) < 3 {
				return true, "usage: /task done <id>", conv
			}
			recordUser(text)
			id, err := strconv.ParseInt(fields[2], 10, 64)
			if err != nil {
				return true, "invalid id", conv
			}
			if err := store.DeleteMemoryItem(g.db, userID, id); err != nil {
				return true, "failed: " + err.Error(), conv
			}
			triggerProactiveEvent(proactive.EventTaskChanged, map[string]string{"text": text})
			return true, "task marked done", conv
		default:
			return true, "usage: /task list | /task add <text> | /task edit <id> <text> | /task done <id>", conv
		}

	case "/secret":
		if len(fields) < 2 {
			return true, "usage: /secret add <label> <secret> | /secret list | /secret delete <label> | /secret clear", conv
		}
		s, err := secrets.NewStore(g.db, g.cfg.Secrets.MasterKey, time.Duration(g.cfg.Secrets.TTLHours)*time.Hour)
		if err != nil {
			return true, "secrets unavailable: " + err.Error() + " (set TETHER_MASTER_KEY; generate with: go run ./cmd/tether-keygen)", conv
		}
		switch fields[1] {
		case "add":
			if len(fields) < 4 {
				return true, "usage: /secret add <label> <secret>", conv
			}
			label := fields[2]
			redactedCmd := "/secret add " + label + " [REDACTED]"
			recordUser(redactedCmd)
			if err := s.Put(context.Background(), userID, label, strings.Join(fields[3:], " ")); err != nil {
				return true, "failed to store secret: " + err.Error(), conv
			}
			exp := time.Now().Add(time.Duration(g.cfg.Secrets.TTLHours) * time.Hour).Format(time.RFC3339)
			return true, "secret stored as '" + label + "' (expires ~" + exp + ")", conv
		case "list":
			recordUser(text)
			items, err := s.List(context.Background(), userID)
			if err != nil {
				return true, "failed to list secrets: " + err.Error(), conv
			}
			if len(items) == 0 {
				return true, "no secrets set", conv
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
			return true, strings.TrimSpace(b.String()), conv
		case "delete":
			if len(fields) < 3 {
				return true, "usage: /secret delete <label>", conv
			}
			recordUser(text)
			if err := s.Delete(context.Background(), userID, fields[2]); err != nil {
				return true, "failed to delete secret: " + err.Error(), conv
			}
			return true, "deleted secret '" + fields[2] + "'", conv
		case "clear":
			recordUser(text)
			if err := s.Clear(context.Background(), userID); err != nil {
				return true, "failed to clear secrets: " + err.Error(), conv
			}
			return true, "cleared all secrets", conv
		default:
			return true, "usage: /secret add <label> <secret> | /secret list | /secret delete <label> | /secret clear", conv
		}

	case "/subagent":
		if len(fields) < 2 {
			return true, "usage: /subagent spawn <prompt> | /subagent status <id>", conv
		}
		switch fields[1] {
		case "spawn":
			prompt := strings.TrimSpace(strings.TrimPrefix(text, "/subagent spawn"))
			if prompt == "" {
				return true, "usage: /subagent spawn <prompt>", conv
			}
			recordUser(text)
			run := g.ag.Subagents().Spawn(userID, prompt)
			return true, "spawned subagent: " + run.ID + " (status: " + string(run.Status) + ")", conv
		case "status":
			if len(fields) < 3 {
				return true, "usage: /subagent status <id>", conv
			}
			recordUser(text)
			run, ok := g.ag.Subagents().Get(fields[2])
			if !ok {
				return true, "subagent not found: " + fields[2], conv
			}
			resp := "subagent " + run.ID + ": " + string(run.Status)
			if run.Err != "" {
				resp += "\nerror: " + run.Err
			}
			if run.Result != "" {
				resp += "\nresult:\n" + run.Result
			}
			return true, resp, conv
		default:
			return true, "usage: /subagent spawn <prompt> | /subagent status <id>", conv
		}

	case "/proactive":
		if len(fields) < 3 {
			return true, "usage: /proactive action <name> | /proactive agent <id>", conv
		}
		switch fields[1] {
		case "action":
			recordUser(text)
			triggerProactiveAction(fields[2], map[string]string{"text": text})
			return true, "triggered proactive action: " + fields[2], conv
		case "agent":
			recordUser(text)
			triggerProactiveAgent(fields[2], map[string]string{"text": text})
			return true, "triggered proactive agent: " + fields[2], conv
		default:
			return true, "usage: /proactive action <name> | /proactive agent <id>", conv
		}

	default:
		recordUser(text)
		return true, "unknown command: " + fields[0] + "\nUse /help for commands.", conv
	}
}
