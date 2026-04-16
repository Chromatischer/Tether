package agent

import (
	"context"

	"tether/internal/subagents"
)

type agentSubagentRunner struct {
	ag *Agent
}

func (r agentSubagentRunner) Run(ctx context.Context, userID int64, prompt string) (string, error) {
	return r.ag.RunPromptForUser(ctx, userID, prompt)
}

// Subagents returns the shared subagent manager.
func (a *Agent) Subagents() *subagents.Manager {
	return a.subMgr
}
