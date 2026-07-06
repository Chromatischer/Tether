package discord

import (
	"context"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"charm.land/log/v2"
	"github.com/bwmarrin/discordgo"

	"tether/internal/agent"
	"tether/internal/chatcmd"
	"tether/internal/config"
	"tether/internal/proactive"
	"tether/internal/redact"
	"tether/internal/store"
	"tether/internal/subagents"
	"tether/internal/userspace"
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

	// fetch downloads attachment URLs; overridable in tests.
	fetch attachmentFetcher

	mu               sync.Mutex
	pendingByMessage map[string]discordPendingReaction
}

type discordPendingReaction struct {
	UserID         int64
	ConversationID int64
	ChannelID      string
	Kind           string
	ChoiceCount    int
}

const (
	discordMessageLimit         = 1900
	discordEditInterval         = 1200 * time.Millisecond
	discordTypingInterval       = 8 * time.Second
	discordStreamingActivity    = "Answering DMs"
	discordStreamingPlaceholder = "..."
	discordReactionConfirm      = "confirm"
	discordReactionChoice       = "choice"
)

var discordChoiceLinePattern = regexp.MustCompile(`(?m)^\s*(10|[1-9])[\.\)]\s+\S`)

func NewGateway(cfg *config.Config, db *sql.DB, ag *agent.Agent) *Gateway {
	return &Gateway{
		cfg:              cfg,
		db:               db,
		ag:               ag,
		fetch:            httpFetch,
		pendingByMessage: map[string]discordPendingReaction{},
	}
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
	s.Identify.Intents = discordgo.IntentsGuilds | discordgo.IntentsDirectMessages | discordgo.IntentsDirectMessageReactions | discordgo.IntentsMessageContent
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
	s.AddHandler(func(s *discordgo.Session, r *discordgo.MessageReactionAdd) {
		go g.onReaction(ctx, s, r)
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
			g.pruneAttachments()
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
	if content == "" && len(m.Attachments) == 0 {
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

	// Ingest any attachments into the user's sandbox and embed references in
	// the turn text. Files are saved before redaction/streaming; only the
	// notation text is redacted, the bytes stay on disk for the agent to read.
	root := userspace.ForUser(g.cfg.Paths.DataDir, uid).Root
	saved, attErrs := g.ingestAttachments(ctx, root, m.Attachments)
	turnText := combineMessageText(content, renderAttachmentBlock(saved, attErrs))
	if strings.TrimSpace(turnText) == "" {
		return
	}

	// Best-effort audit without storing message content.
	sum := sha256.Sum256([]byte(content))
	auditPayload := map[string]any{"from": discordUID, "len": len(content), "sha256": hex.EncodeToString(sum[:])}
	if len(saved) > 0 || len(attErrs) > 0 {
		files := make([]map[string]any, 0, len(saved))
		for _, a := range saved {
			files = append(files, map[string]any{"kind": string(a.Kind), "sha256": a.SHA256, "size": a.Size, "type": a.ContentType})
		}
		auditPayload["attachments"] = files
		auditPayload["attachment_errors"] = len(attErrs)
	}
	payload, _ := json.Marshal(auditPayload)
	_ = store.AddAuditEvent(g.db, &uid, "discord_inbound", string(payload))

	g.handleConversationTurn(ctx, s, uid, conv.ID, m.ChannelID, turnText, discordUID)
}

func (g *Gateway) sendChunks(s *discordgo.Session, channelID string, msg string) {
	sendDiscordChunks(s, channelID, msg)
}

// introductionMessage is sent as a DM to a Discord user right after their
// account is linked. Edit internal/discord/INTRODUCTION.md to change it.
//
//go:embed INTRODUCTION.md
var introductionMessage string

// SendIntroduction DMs the post-link welcome message to a freshly linked
// Discord user. It is best-effort: a no-op when the gateway is disabled.
func (g *Gateway) SendIntroduction(discordUserID string) error {
	return g.SendDM(discordUserID, introductionMessage)
}

// DeliverMessage implements proactive.Notifier: it pushes a proactively-generated
// message to the user's linked Discord DM. Returns false (not an error) when the
// gateway isn't running or the user has no linked Discord account.
func (g *Gateway) DeliverMessage(ctx context.Context, userID, conversationID int64, text string) (bool, error) {
	_ = ctx
	_ = conversationID
	if g == nil || g.s == nil {
		return false, nil
	}
	discordUserID, ok, err := store.GetDiscordUserID(g.db, userID)
	if err != nil {
		return false, err
	}
	if !ok || strings.TrimSpace(discordUserID) == "" {
		return false, nil
	}
	if err := g.SendDM(discordUserID, text); err != nil {
		return false, err
	}
	return true, nil
}

// SendDM opens (or reuses) a DM channel with the given Discord user and sends
// content, chunking as needed. Returns nil without error when the gateway is
// not running so callers can treat it as best-effort.
func (g *Gateway) SendDM(discordUserID, content string) error {
	if g == nil {
		return nil
	}
	s := g.s
	if s == nil {
		return nil
	}
	discordUserID = strings.TrimSpace(discordUserID)
	if discordUserID == "" {
		return fmt.Errorf("empty discord user id")
	}
	ch, err := s.UserChannelCreate(discordUserID)
	if err != nil {
		return fmt.Errorf("create dm channel: %w", err)
	}
	g.sendChunks(s, ch.ID, content)
	return nil
}

func (g *Gateway) onReaction(ctx context.Context, s *discordgo.Session, r *discordgo.MessageReactionAdd) {
	if r == nil || r.MessageReaction == nil {
		return
	}
	if g.s != nil && g.s.State != nil && g.s.State.User != nil && r.UserID == g.s.State.User.ID {
		return
	}

	g.mu.Lock()
	pending, ok := g.pendingByMessage[r.MessageID]
	g.mu.Unlock()
	if !ok || pending.ChannelID != r.ChannelID {
		return
	}

	uid, linked, err := store.FindUserIDByDiscordUserID(g.db, strings.TrimSpace(r.UserID))
	if err != nil || !linked || uid != pending.UserID {
		return
	}

	emoji := strings.TrimSpace(r.Emoji.Name)
	switch pending.Kind {
	case discordReactionConfirm:
		if emoji != "✅" && emoji != "❌" {
			return
		}
		g.clearPendingReaction(r.ChannelID, r.MessageID)
		if emoji == "✅" {
			token, ok := g.ag.PendingConfirmationToken(pending.UserID, pending.ConversationID)
			if !ok {
				return
			}
			stream := newDiscordReplyStream(ctx, s, r.ChannelID, store.GetDiscordVerbosity(g.db, pending.UserID))
			defer stream.Close()
			stream.Start()
			reply, convID, resumed, err := g.ag.ResumeConfirmedStream(ctx, pending.UserID, token, func(ev agent.StreamEvent) {
				stream.OnEvent(ev)
			})
			if err != nil {
				stream.Finish("Agent error: " + err.Error())
				return
			}
			if !resumed {
				stream.Finish("confirmation failed")
				return
			}
			out, of := redact.ScanAndRedact(reply.Text)
			_, _ = store.AddAssistantMessageWithReasoning(g.db, convID, out, g.ag.Model(), reply.ReasoningItems)
			if len(of) > 0 {
				stream.Finish("(Assistant response was redacted due to secret-like content.)\n" + out)
				return
			}
			g.finishWithAttachments(stream, pending.UserID, out)
			g.attachPendingConfirmationReaction(pending.UserID, convID, r.ChannelID, stream.MessageID())
			g.attachPendingChoiceReactions(pending.UserID, convID, r.ChannelID, stream.MessageID(), out)
			return
		}

		if !g.ag.RejectPendingConfirmation(pending.UserID, pending.ConversationID) {
			return
		}
		note := "Pending tool confirmation rejected by the user."
		_ = store.AddMessage(g.db, pending.ConversationID, "system", note)
		stream := newDiscordReplyStream(ctx, s, r.ChannelID, store.GetDiscordVerbosity(g.db, pending.UserID))
		defer stream.Close()
		stream.Start()
		reply, err := g.ag.ReplyStream(ctx, agent.ReplyParams{UserID: pending.UserID, ConversationID: pending.ConversationID, Text: ""}, func(ev agent.StreamEvent) {
			stream.OnEvent(ev)
		})
		if err != nil {
			stream.Finish("Agent error: " + err.Error())
			return
		}
		out, of := redact.ScanAndRedact(reply.Text)
		_, _ = store.AddAssistantMessageWithReasoning(g.db, pending.ConversationID, out, g.ag.Model(), reply.ReasoningItems)
		if len(of) > 0 {
			stream.Finish("(Assistant response was redacted due to secret-like content.)\n" + out)
			return
		}
		g.finishWithAttachments(stream, pending.UserID, out)
		g.attachPendingConfirmationReaction(pending.UserID, pending.ConversationID, r.ChannelID, stream.MessageID())
		g.attachPendingChoiceReactions(pending.UserID, pending.ConversationID, r.ChannelID, stream.MessageID(), out)
	case discordReactionChoice:
		choice, ok := discordChoiceNumberFromEmoji(emoji)
		if !ok || choice < 1 || choice > pending.ChoiceCount {
			return
		}
		g.clearPendingReaction(r.ChannelID, r.MessageID)
		payload, _ := json.Marshal(map[string]any{"choice": choice, "message_id": r.MessageID})
		_ = store.AddAuditEvent(g.db, &uid, "discord_reaction_choice", string(payload))
		g.handleConversationTurn(ctx, s, uid, pending.ConversationID, r.ChannelID, strconv.Itoa(choice), strings.TrimSpace(r.UserID))
	}
}

func (g *Gateway) handleConversationTurn(ctx context.Context, s *discordgo.Session, uid, convID int64, channelID, content, remoteID string) {
	clean, findings := redact.ScanAndRedact(content)
	if g.ag.HasPendingConfirmation(uid, convID) {
		note := "Pending tool confirmation rejected by the user."
		_ = g.ag.RejectPendingConfirmation(uid, convID)
		_ = store.AddMessage(g.db, convID, "system", note)
	}
	_ = store.AddMessage(g.db, convID, "user", clean)
	if len(findings) > 0 {
		_, _ = s.ChannelMessageSend(channelID, "Your message looked like it contained secrets/tokens and was redacted. Please use /secret add via SSH for secrets.")
	}

	stream := newDiscordReplyStream(ctx, s, channelID, store.GetDiscordVerbosity(g.db, uid))
	defer stream.Close()
	stream.Start()

	reply, err := g.ag.ReplyStream(ctx, agent.ReplyParams{UserID: uid, ConversationID: convID, Text: clean}, func(ev agent.StreamEvent) {
		stream.OnEvent(ev)
	})
	if err != nil {
		log.Warn("agent reply failed", "error", err, "user_id", uid, "conversation_id", convID, "discord_channel", channelID)
		stream.Finish("Agent error: " + err.Error())
		return
	}

	out, of := redact.ScanAndRedact(reply.Text)
	_, _ = store.AddAssistantMessageWithReasoning(g.db, convID, out, g.ag.Model(), reply.ReasoningItems)

	sum := sha256.Sum256([]byte(out))
	payload, _ := json.Marshal(map[string]any{"to": remoteID, "len": len(out), "sha256": hex.EncodeToString(sum[:])})
	_ = store.AddAuditEvent(g.db, &uid, "discord_send", string(payload))

	if len(of) > 0 {
		stream.Finish("(Assistant response was redacted due to secret-like content.)\n" + out)
		return
	}
	g.finishWithAttachments(stream, uid, out)
	g.attachPendingConfirmationReaction(uid, convID, channelID, stream.MessageID())
	g.attachPendingChoiceReactions(uid, convID, channelID, stream.MessageID(), out)
}

func (g *Gateway) attachPendingChoiceReactions(userID, convID int64, channelID, messageID, text string) {
	messageID = strings.TrimSpace(messageID)
	if messageID == "" {
		return
	}
	choiceCount := detectDiscordChoiceCount(text)
	if choiceCount < 2 {
		return
	}
	for i := 1; i <= choiceCount; i++ {
		if emoji, ok := discordChoiceEmoji(i); ok {
			_ = g.s.MessageReactionAdd(channelID, messageID, emoji)
		}
	}
	g.mu.Lock()
	g.pendingByMessage[messageID] = discordPendingReaction{
		UserID:         userID,
		ConversationID: convID,
		ChannelID:      channelID,
		Kind:           discordReactionChoice,
		ChoiceCount:    choiceCount,
	}
	g.mu.Unlock()
}

func (g *Gateway) attachPendingConfirmationReaction(userID, convID int64, channelID string, messageID string) {
	messageID = strings.TrimSpace(messageID)
	if messageID == "" {
		return
	}
	if !g.ag.HasPendingConfirmation(userID, convID) {
		return
	}
	_ = g.s.MessageReactionAdd(channelID, messageID, "✅")
	_ = g.s.MessageReactionAdd(channelID, messageID, "❌")
	g.mu.Lock()
	g.pendingByMessage[messageID] = discordPendingReaction{
		UserID:         userID,
		ConversationID: convID,
		ChannelID:      channelID,
		Kind:           discordReactionConfirm,
	}
	g.mu.Unlock()
}

func (g *Gateway) clearPendingReaction(channelID string, messageID string) {
	g.mu.Lock()
	pending, ok := g.pendingByMessage[messageID]
	if ok {
		delete(g.pendingByMessage, messageID)
	}
	g.mu.Unlock()
	if !ok {
		return
	}
	switch pending.Kind {
	case discordReactionConfirm:
		_ = g.s.MessageReactionRemove(channelID, messageID, "✅", "@me")
		_ = g.s.MessageReactionRemove(channelID, messageID, "❌", "@me")
	case discordReactionChoice:
		for i := 1; i <= pending.ChoiceCount; i++ {
			if emoji, ok := discordChoiceEmoji(i); ok {
				_ = g.s.MessageReactionRemove(channelID, messageID, emoji, "@me")
			}
		}
	}
}

func discordHelpSubcommand(sub string) string {
	subHelp := map[string][]string{
		"tools":     {"/tools list", "/tools search <query>", "/tools describe <name>"},
		"subagent":  {"/subagent spawn <prompt>", "/subagent status <id>"},
		"proactive": {"/proactive action <name>", "/proactive agent <id>"},
		"signal":    {"/signal status"},
		"discord":   {"/discord status", "/discord unlink"},
		"memory":    {"/memory list [kind]", "/memory add <kind> <content>", "/memory update <id> <content>", "/memory delete <id>"},
		"task":      {"/task list", "/task add <text>", "/task edit <id> <text>", "/task done <id>"},
		"secret":    {"/secret add <label> <secret>", "/secret list", "/secret delete <label>", "/secret clear"},
	}
	if lines, ok := subHelp[sub]; ok {
		return strings.Join(lines, "\n")
	}
	return "unknown command: " + sub
}

func detectDiscordChoiceCount(text string) int {
	text = strings.TrimSpace(text)
	if text == "" {
		return 0
	}
	lower := strings.ToLower(text)
	if !strings.Contains(lower, "reply with just the number") &&
		!strings.Contains(lower, "respond with just the number") &&
		!strings.Contains(lower, "reply with the number") &&
		!strings.Contains(lower, "respond with the number") {
		return 0
	}
	matches := discordChoiceLinePattern.FindAllStringSubmatch(text, -1)
	if len(matches) < 2 {
		return 0
	}
	seen := map[int]bool{}
	maxChoice := 0
	for _, match := range matches {
		n, err := strconv.Atoi(strings.TrimSpace(match[1]))
		if err != nil || n < 1 || n > 10 {
			return 0
		}
		seen[n] = true
		if n > maxChoice {
			maxChoice = n
		}
	}
	for i := 1; i <= maxChoice; i++ {
		if !seen[i] {
			return 0
		}
	}
	return maxChoice
}

func discordChoiceEmoji(n int) (string, bool) {
	switch n {
	case 1:
		return "1️⃣", true
	case 2:
		return "2️⃣", true
	case 3:
		return "3️⃣", true
	case 4:
		return "4️⃣", true
	case 5:
		return "5️⃣", true
	case 6:
		return "6️⃣", true
	case 7:
		return "7️⃣", true
	case 8:
		return "8️⃣", true
	case 9:
		return "9️⃣", true
	case 10:
		return "🔟", true
	default:
		return "", false
	}
}

func discordChoiceNumberFromEmoji(emoji string) (int, bool) {
	switch strings.TrimSpace(emoji) {
	case "1️⃣":
		return 1, true
	case "2️⃣":
		return 2, true
	case "3️⃣":
		return 3, true
	case "4️⃣":
		return 4, true
	case "5️⃣":
		return 5, true
	case "6️⃣":
		return 6, true
	case "7️⃣":
		return 7, true
	case "8️⃣":
		return 8, true
	case "9️⃣":
		return 9, true
	case "🔟":
		return 10, true
	default:
		return 0, false
	}
}

type discordReplyStream struct {
	ctx       context.Context
	cancel    context.CancelFunc
	s         *discordgo.Session
	channel   string
	verbosity string
	ticker    *time.Ticker

	mu        sync.Mutex
	messageID string
	lastSent  string
	lastEdit  time.Time
	dirty     bool

	reasoning strings.Builder
	answer    strings.Builder
	tools     []string
	finalText string
	files     []*discordgo.File
}

func newDiscordReplyStream(parent context.Context, s *discordgo.Session, channelID, verbosity string) *discordReplyStream {
	ctx, cancel := context.WithCancel(parent)
	if !store.ValidDiscordVerbosity(verbosity) {
		verbosity = store.DiscordVerbosityDefault
	}
	return &discordReplyStream{
		ctx:       ctx,
		cancel:    cancel,
		s:         s,
		channel:   channelID,
		verbosity: verbosity,
		ticker:    time.NewTicker(discordEditInterval),
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
		// Reasoning is only surfaced at the most verbose level.
		if r.verbosity == store.DiscordVerbosityFull {
			r.reasoning.WriteString(ev.Delta)
			r.dirty = true
		}
	case "assistant_delta":
		r.answer.WriteString(ev.Delta)
		r.dirty = true
	case "tool_call":
		// Tool calls are hidden in message-only mode.
		if r.verbosity != store.DiscordVerbosityMessageOnly {
			if name := strings.TrimSpace(ev.Tool.Name); name != "" {
				r.tools = append(r.tools, name)
				r.dirty = true
			}
		}
	}
	r.mu.Unlock()
	r.flush(false)
}

// FinishWithFiles finalizes the reply and attaches files to the final message.
func (r *discordReplyStream) FinishWithFiles(text string, files []*discordgo.File) {
	r.mu.Lock()
	r.files = files
	r.mu.Unlock()
	r.Finish(text)
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
	files := r.files
	r.mu.Unlock()

	chunks := splitDiscordMessage(content, discordMessageLimit)
	if len(chunks) == 0 {
		// A file-only reply needs a placeholder-free message; otherwise show the
		// usual empty-response note.
		if len(files) == 0 {
			chunks = []string{"(No response.)"}
		} else {
			chunks = []string{""}
		}
	}

	lastIdx := len(chunks) - 1
	for i, chunk := range chunks {
		var chunkFiles []*discordgo.File
		if i == lastIdx {
			chunkFiles = files
		}

		// Reuse the streamed placeholder for the first chunk.
		if i == 0 && messageID != "" {
			edit := discordgo.NewMessageEdit(channelID, messageID)
			edit.SetContent(chunk)
			if len(chunkFiles) > 0 {
				edit.Files = chunkFiles
			}
			if _, err := r.s.ChannelMessageEditComplex(edit); err != nil {
				log.Warn("failed to edit discord final message", "error", err)
				r.sendChunkWithFiles(channelID, chunk, chunkFiles)
			} else {
				r.mu.Lock()
				r.lastSent = chunk
				r.lastEdit = time.Now()
				r.mu.Unlock()
			}
			continue
		}
		r.sendChunkWithFiles(channelID, chunk, chunkFiles)
	}
}

func (r *discordReplyStream) sendChunkWithFiles(channelID, content string, files []*discordgo.File) {
	if len(files) == 0 {
		_, _ = r.s.ChannelMessageSend(channelID, content)
		return
	}
	_, _ = r.s.ChannelMessageSendComplex(channelID, &discordgo.MessageSend{Content: content, Files: files})
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

func (r *discordReplyStream) MessageID() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return strings.TrimSpace(r.messageID)
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
		eng := proactive.NewEngine(g.db, g.ag, g.ag, g.ag.Subagents(), g.cfg.Paths.DataDir)
		go func() {
			ctx2, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			eng.TriggerEvent(ctx2, userID, event, meta)
		}()
	}
	triggerProactiveAction := func(action string, meta map[string]string) {
		eng := proactive.NewEngine(g.db, g.ag, g.ag, g.ag.Subagents(), g.cfg.Paths.DataDir)
		go func() {
			ctx2, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			eng.TriggerAction(ctx2, userID, action, meta)
		}()
	}
	triggerProactiveAgent := func(agentID string, meta map[string]string) {
		eng := proactive.NewEngine(g.db, g.ag, g.ag, g.ag.Subagents(), g.cfg.Paths.DataDir)
		go func() {
			ctx2, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			eng.TriggerAgent(ctx2, userID, agentID, meta)
		}()
	}

	switch fields[0] {
	case "/help":
		recordUser(text)
		var resp string
		if len(fields) >= 2 {
			resp = discordHelpSubcommand(fields[1])
		} else {
			resp = strings.TrimSpace(
				"Commands:\n" +
					"  /status\n" +
					"  /clear\n" +
					"  /resume <code>\n" +
					"  /confirm <token>\n" +
					"  /help [command]\n" +
					"  /tools <list|search|describe>\n" +
					"  /subagent <spawn|status>\n" +
					"  /proactive <action|agent>\n" +
					"  /signal <status>\n" +
					"  /discord <status|unlink>\n" +
					"  /memory <list|add|update|delete>\n" +
					"  /task <list|add|edit|done>\n" +
					"  /secret <add|list|delete|clear>",
			)
		}
		return true, resp, conv

	case "/status":
		recordUser(text)
		return true, renderSessionStatusDiscord(g.ag.SessionStatus(userID, conv.ID)), conv

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

		// Default behavior: if the token is tied to a suspended tool execution, resume it.
		// Otherwise treat /confirm as a standalone confirmation (e.g. tokens created via confirm.request).
		if g.ag.HasPendingConfirmationToken(userID, fields[1]) {
			reply, _, resumed, err := g.ag.ResumeConfirmedStream(ctx, userID, fields[1], nil)
			if err != nil {
				return true, "agent error: " + err.Error(), conv
			}
			if resumed {
				out, findings := redact.ScanAndRedact(reply.Text)
				if len(findings) > 0 {
					return true, "(Assistant response was redacted due to secret-like content.)\n" + out, conv
				}
				return true, out, conv
			}
			return true, "confirmation failed", conv
		}

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
			return true, chatcmd.ToolsDescribe(reg, fields[2]), conv
		}
		if len(fields) == 1 || fields[1] == "list" {
			return true, chatcmd.ToolsList(reg), conv
		} else if fields[1] == "search" {
			return true, chatcmd.ToolsSearch(reg, strings.Join(fields[2:], " ")), conv
		}
		return true, "usage: /tools list | /tools search <query> | /tools describe <name>", conv

	case "/signal":
		if len(fields) < 2 {
			return true, "usage: /signal link | /signal status | /signal unlink", conv
		}
		switch fields[1] {
		case "link":
			recordUser(text)
			return true, chatcmd.SignalLink(g.db, userID, g.cfg.Signal.AccountNumber), conv
		case "status":
			recordUser(text)
			return true, chatcmd.SignalStatus(g.db, userID), conv
		case "unlink":
			recordUser(text)
			return true, chatcmd.SignalUnlink(g.db, userID), conv
		default:
			return true, "usage: /signal link | /signal status | /signal unlink", conv
		}

	case "/discord":
		if len(fields) < 2 {
			return true, "usage: /discord status | /discord unlink | /discord verbosity [full|no_thinking|message_only]", conv
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
			return true, "Discord linked: " + did + "\nVerbosity: " + store.GetDiscordVerbosity(g.db, userID), conv
		case "unlink":
			recordUser(text)
			if err := store.UnlinkDiscordUserID(g.db, userID); err != nil {
				return true, "failed to unlink: " + err.Error(), conv
			}
			return true, "Discord unlinked", conv
		case "verbosity":
			recordUser(text)
			if len(fields) < 3 {
				return true, "Discord verbosity: " + store.GetDiscordVerbosity(g.db, userID) +
					"\nusage: /discord verbosity [full|no_thinking|message_only]\n" +
					"  full — show tool calls, reasoning, and the final message\n" +
					"  no_thinking — show tool calls and the final message\n" +
					"  message_only — show only the final message", conv
			}
			level := strings.ToLower(strings.TrimSpace(fields[2]))
			if err := store.SetDiscordVerbosity(g.db, userID, level); err != nil {
				return true, "failed to set verbosity: " + err.Error() + "\nchoose one of: full, no_thinking, message_only", conv
			}
			return true, "Discord verbosity set to " + level, conv
		default:
			return true, "usage: /discord status | /discord unlink | /discord verbosity [full|no_thinking|message_only]", conv
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
			return true, chatcmd.MemoryList(g.db, userID, kind), conv
		case "add":
			if len(fields) < 4 {
				return true, "usage: /memory add <kind> <content>", conv
			}
			recordUser(text)
			return true, chatcmd.MemoryAdd(g.db, userID, fields[2], strings.Join(fields[3:], " ")), conv
		case "update":
			if len(fields) < 4 {
				return true, "usage: /memory update <id> <content>", conv
			}
			recordUser(text)
			return true, chatcmd.MemoryUpdate(g.db, userID, fields[2], strings.Join(fields[3:], " ")), conv
		case "delete":
			if len(fields) < 3 {
				return true, "usage: /memory delete <id>", conv
			}
			recordUser(text)
			return true, chatcmd.MemoryDelete(g.db, userID, fields[2]), conv
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
			return true, chatcmd.TaskList(g.db, userID), conv
		case "add":
			if len(fields) < 3 {
				return true, "usage: /task add <text>", conv
			}
			recordUser(text)
			content := strings.Join(fields[2:], " ")
			resp, ok := chatcmd.TaskAdd(g.db, userID, content)
			if ok {
				triggerProactiveEvent(proactive.EventTaskChanged, map[string]string{"text": content})
			}
			return true, resp, conv
		case "edit":
			if len(fields) < 4 {
				return true, "usage: /task edit <id> <text>", conv
			}
			recordUser(text)
			content := strings.Join(fields[3:], " ")
			resp, ok := chatcmd.TaskUpdate(g.db, userID, fields[2], content)
			if ok {
				triggerProactiveEvent(proactive.EventTaskChanged, map[string]string{"text": content})
			}
			return true, resp, conv
		case "done":
			if len(fields) < 3 {
				return true, "usage: /task done <id>", conv
			}
			recordUser(text)
			resp, ok := chatcmd.TaskDone(g.db, userID, fields[2])
			if ok {
				triggerProactiveEvent(proactive.EventTaskChanged, map[string]string{"text": text})
			}
			return true, resp, conv
		default:
			return true, "usage: /task list | /task add <text> | /task edit <id> <text> | /task done <id>", conv
		}

	case "/secret":
		if len(fields) < 2 {
			return true, "usage: /secret add <label> <secret> | /secret list | /secret delete <label> | /secret clear", conv
		}
		s, errMsg := chatcmd.OpenSecretStore(g.db, g.cfg.Secrets.MasterKey, g.cfg.Secrets.TTLHours)
		if s == nil {
			return true, errMsg, conv
		}
		ttl := g.cfg.Secrets.TTLHours
		switch fields[1] {
		case "add":
			if len(fields) < 4 {
				return true, "usage: /secret add <label> <secret>", conv
			}
			label := fields[2]
			recordUser("/secret add " + label + " [REDACTED]")
			return true, chatcmd.SecretAdd(s, userID, label, strings.Join(fields[3:], " "), ttl), conv
		case "list":
			recordUser(text)
			return true, chatcmd.SecretList(s, userID), conv
		case "delete":
			if len(fields) < 3 {
				return true, "usage: /secret delete <label>", conv
			}
			recordUser(text)
			return true, chatcmd.SecretDelete(s, userID, fields[2]), conv
		case "clear":
			recordUser(text)
			return true, chatcmd.SecretClear(s, userID), conv
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
			run := g.ag.Subagents().Spawn(userID, subagents.RunRequest{Prompt: prompt})
			return true, "spawned subagent: " + run.ID + " (status: " + string(run.Status) + ")", conv
		case "status":
			if len(fields) < 3 {
				return true, "usage: /subagent status <id>", conv
			}
			recordUser(text)
			run, ok := g.ag.Subagents().GetForUser(userID, fields[2])
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
