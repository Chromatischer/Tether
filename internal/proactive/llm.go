package proactive

import "context"

// LLM is the minimal interface proactive code needs from the agent runtime.
// (Implemented by *agent.Agent.)
type LLM interface {
	RunPrompt(ctx context.Context, prompt string) (string, error)
	RunProactivePrompt(ctx context.Context, prompt string) (string, error)
}
