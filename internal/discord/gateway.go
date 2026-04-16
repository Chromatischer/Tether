package discord

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	"charm.land/log/v2"
	"github.com/bwmarrin/discordgo"

	"tether/internal/agent"
	"tether/internal/config"
	"tether/internal/redact"
	"tether/internal/store"
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

func NewGateway(cfg *config.Config, db *sql.DB, ag *agent.Agent) *Gateway {
	return &Gateway{cfg: cfg, db: db, ag: ag}
}

func (g *Gateway) Start(ctx context.Context) {
	if !g.cfg.Discord.Enabled {
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

	// Required to receive DM message events + content.
	// Note: MessageContent is a privileged intent and must be enabled in the Discord developer portal.
	s.Identify.Intents = discordgo.IntentsDirectMessages | discordgo.IntentsMessageContent

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

	conv, err := store.GetOrCreateDefaultConversation(g.db, uid)
	if err != nil {
		_, _ = s.ChannelMessageSend(m.ChannelID, "Internal error.")
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

	reply, err := g.ag.Reply(ctx, agent.ReplyParams{UserID: uid, ConversationID: conv.ID, Text: clean})
	if err != nil {
		_, _ = s.ChannelMessageSend(m.ChannelID, "Agent error: "+err.Error())
		return
	}

	out, of := redact.ScanAndRedact(reply.Text)
	_ = store.AddMessage(g.db, conv.ID, "assistant", out)

	// Best-effort audit without storing message content.
	sum2 := sha256.Sum256([]byte(out))
	payload2, _ := json.Marshal(map[string]any{"to": discordUID, "len": len(out), "sha256": hex.EncodeToString(sum2[:])})
	_ = store.AddAuditEvent(g.db, &uid, "discord_send", string(payload2))

	if len(of) > 0 {
		g.sendChunks(s, m.ChannelID, "(Assistant response was redacted due to secret-like content.)\n"+out)
		return
	}
	g.sendChunks(s, m.ChannelID, out)
}

func (g *Gateway) sendChunks(s *discordgo.Session, channelID string, msg string) {
	msg = strings.TrimSpace(msg)
	if msg == "" {
		return
	}
	const max = 1900
	for len(msg) > max {
		chunk := msg[:max]
		// Try not to split in the middle of a line.
		if i := strings.LastIndex(chunk, "\n"); i > 400 {
			chunk = chunk[:i]
		}
		_, _ = s.ChannelMessageSend(channelID, chunk)
		msg = strings.TrimLeft(msg[len(chunk):], "\n")
	}
	_, _ = s.ChannelMessageSend(channelID, msg)
}
