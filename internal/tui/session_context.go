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

	// AutoLogin skips the login/signup view and enters as the local root
	// account. Used by terminal mode, which has no portal authentication.
	AutoLogin bool

	// ConnectorsLive is true when the Signal/Discord gateways and proactive
	// scheduler are running in this process (server mode). Terminal mode runs
	// the TUI standalone with no connectors, so the header must not claim them
	// as online.
	ConnectorsLive bool
}

func NewSessionContext(s ssh.Session, cfg *config.Config, db *sql.DB, ag *agent.Agent, discord DiscordNotifier) *SessionContext {
	return &SessionContext{SSH: s, Term: DetectTerminalProfile(s), Config: cfg, DB: db, Agent: ag, Discord: discord, ConnectorsLive: true}
}

// NewLocalSessionContext builds a context for running the TUI directly in the
// attached terminal (no SSH session). SSH is nil; the TUI never calls into it.
func NewLocalSessionContext(cfg *config.Config, db *sql.DB, ag *agent.Agent) *SessionContext {
	return &SessionContext{SSH: nil, Term: DetectLocalTerminalProfile(), Config: cfg, DB: db, Agent: ag, AutoLogin: true}
}
