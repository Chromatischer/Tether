package tui

import (
	"database/sql"

	"github.com/charmbracelet/ssh"

	"tether/internal/agent"
	"tether/internal/config"
)

// DiscordNotifier sends out-of-band messages to a linked Discord user.
// The discord gateway implements this; it is nil when Discord is disabled.
type DiscordNotifier interface {
	// SendIntroduction DMs the post-link welcome message to a Discord user.
	SendIntroduction(discordUserID string) error
}

// SessionContext holds per-SSH-session dependencies.
// Portal auth is handled at the SSH layer; this context is for the in-app session.
type SessionContext struct {
	SSH     ssh.Session
	Term    TerminalProfile
	Config  *config.Config
	DB      *sql.DB
	Agent   *agent.Agent
	Discord DiscordNotifier
}

func NewSessionContext(s ssh.Session, cfg *config.Config, db *sql.DB, ag *agent.Agent, discord DiscordNotifier) *SessionContext {
	return &SessionContext{SSH: s, Term: DetectTerminalProfile(s), Config: cfg, DB: db, Agent: ag, Discord: discord}
}
