package portal

import (
	"context"
	"database/sql"
	"net"
	"os"

	tea "charm.land/bubbletea/v2"
	"charm.land/log/v2"
	"charm.land/wish/v2"
	"charm.land/wish/v2/activeterm"
	wishbubbletea "charm.land/wish/v2/bubbletea"
	"charm.land/wish/v2/logging"
	"charm.land/wish/v2/recover"
	"github.com/charmbracelet/ssh"
	"golang.org/x/crypto/bcrypt"

	"tether/internal/agent"
	"tether/internal/config"
	"tether/internal/tui"
)

var ErrServerClosed = ssh.ErrServerClosed

type Server struct {
	s *ssh.Server
}

func NewServer(cfg *config.Config, db *sql.DB, ag *agent.Agent) (*Server, error) {
	passwordHandler := func(_ ssh.Context, password string) bool {
		return bcrypt.CompareHashAndPassword([]byte(cfg.SSH.PortalPasswordHash), []byte(password)) == nil
	}

	opts := []ssh.Option{
		wish.WithAddress(cfg.SSH.ListenAddr),
		wish.WithHostKeyPath(cfg.SSH.HostKeyPath),
		wish.WithPasswordAuth(passwordHandler),
		wish.WithMiddleware(
			wishbubbletea.Middleware(func(s ssh.Session) (tea.Model, []tea.ProgramOption) {
				// Bubble Tea v2 controls alt screen + mouse via tea.View fields.
				// We keep ProgramOptions empty so the model controls its own rendering.
				ctx := tui.NewSessionContext(s, cfg, db, ag)
				return tui.NewAppModel(ctx), nil
			}),
			activeterm.Middleware(),
			recover.Middleware(),
			logging.Middleware(),
		),
	}

	// Optional: authorized_keys allowlist.
	if _, err := os.Stat(cfg.SSH.AuthorizedKeysPath); err == nil {
		opts = append(opts, wish.WithAuthorizedKeys(cfg.SSH.AuthorizedKeysPath))
	} else {
		log.Warn("authorized_keys missing; portal public key auth disabled", "path", cfg.SSH.AuthorizedKeysPath, "error", err)
	}

	server, err := wish.NewServer(opts...)
	if err != nil {
		return nil, err
	}

	// If listen addr is just :port, bind on all interfaces explicitly.
	if host, port, err := net.SplitHostPort(cfg.SSH.ListenAddr); err == nil && host == "" {
		server.Addr = net.JoinHostPort("0.0.0.0", port)
	}

	return &Server{s: server}, nil
}

func (s *Server) ListenAndServe() error {
	return s.s.ListenAndServe()
}

func (s *Server) Shutdown(ctx context.Context) error {
	return s.s.Shutdown(ctx)
}
