package agent

import "tether/internal/tools"

// ToolRegistry returns the registry of tool specifications available to the agent.
func (a *Agent) ToolRegistry() *tools.Registry {
	return a.registry
}
