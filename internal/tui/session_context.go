package tui

import (
	"database/sql"

	"github.com/charmbracelet/ssh"

	"tether/internal/agent"
	"tether/internal/config"
)

// SessionContext holds per-SSH-session dependencies.
// Portal auth is handled at the SSH layer; this context is for the in-app session.
type SessionContext struct {
	SSH    ssh.Session
	Term   TerminalProfile
	Config *config.Config
	DB     *sql.DB
	Agent  *agent.Agent

	// AutoLogin skips the login/signup view and enters as the local root
	// account. Used by terminal mode, which has no portal authentication.
	AutoLogin bool
}

func NewSessionContext(s ssh.Session, cfg *config.Config, db *sql.DB, ag *agent.Agent) *SessionContext {
	return &SessionContext{SSH: s, Term: DetectTerminalProfile(s), Config: cfg, DB: db, Agent: ag}
}

// NewLocalSessionContext builds a context for running the TUI directly in the
// attached terminal (no SSH session). SSH is nil; the TUI never calls into it.
func NewLocalSessionContext(cfg *config.Config, db *sql.DB, ag *agent.Agent) *SessionContext {
	return &SessionContext{SSH: nil, Term: DetectLocalTerminalProfile(), Config: cfg, DB: db, Agent: ag, AutoLogin: true}
}
