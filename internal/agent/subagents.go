package agent

import (
	"context"

	"tether/internal/subagents"
)

type agentSubagentRunner struct {
	ag *Agent
}

func (r agentSubagentRunner) Run(ctx context.Context, userID int64, prompt string) (string, error) {
	_ = userID
	return r.ag.RunPrompt(ctx, prompt)
}

// Subagents returns the shared subagent manager.
func (a *Agent) Subagents() *subagents.Manager {
	return a.subMgr
}
