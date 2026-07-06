package agent

import "tether/internal/tools"

// ToolRegistry returns the registry of tool specifications available to the agent.
func (a *Agent) ToolRegistry() *tools.Registry {
	return a.registry
}

// Model returns the currently configured chat model id. Callers use it to scope
// persisted signed reasoning to the model that produced it.
func (a *Agent) Model() string {
	return a.cfg.OpenRouter.Model
}
