// Package term runs the Tether TUI directly in the attached terminal,
// serving the same interface as the SSH portal without requiring an SSH
// connection. Connectors (Discord, Signal) are not started in this mode.
package term

import (
	"database/sql"

	tea "charm.land/bubbletea/v2"

	"tether/internal/agent"
	"tether/internal/config"
	"tether/internal/tui"
)

// Run launches the local TUI against stdin/stdout. It builds the same app
// model the SSH portal serves per session, but with a local session context.
func Run(cfg *config.Config, database *sql.DB, ag *agent.Agent) error {
	ctx := tui.NewLocalSessionContext(cfg, database, ag)
	// ProgramOptions stay empty: the model controls alt screen and mouse via
	// tea.View, matching the SSH portal's bubbletea middleware.
	_, err := tea.NewProgram(tui.NewAppModel(ctx)).Run()
	return err
}
