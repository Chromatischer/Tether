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
}

func NewSessionContext(s ssh.Session, cfg *config.Config, db *sql.DB, ag *agent.Agent) *SessionContext {
	return &SessionContext{SSH: s, Term: DetectTerminalProfile(s), Config: cfg, DB: db, Agent: ag}
}
