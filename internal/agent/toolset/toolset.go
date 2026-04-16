package toolset

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"tether/internal/tools"
	"tether/internal/userspace"
)

// ToolDef is an OpenAI-style tool definition (subset).
// We'll use this when calling OpenRouter.
type ToolDef struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Parameters  interface{} `json:"parameters"`
}

type Tool interface {
	Definition() ToolDef
	Execute(ctx context.Context, s *Session, rawArgs json.RawMessage) (any, error)
}

// Session represents a per-user agent session (active tools, per-user config, etc.).
type SubagentStore interface {
	Spawn(userID int64, prompt string) (id string)
	Status(id string) (status any, ok bool)
}

type Confirmer interface {
	// Request creates a single-use confirmation token scoped to one specific action.
	// The returned token must be confirmed by the user via /confirm <token>.
	Request(userID int64, scope string, reason string) string
	// Consume marks a confirmed token as used. The token must match the same scope
	// it was requested for.
	Consume(userID int64, token string, scope string) bool
}

type SecretGetter interface {
	Get(ctx context.Context, userID int64, label string) (string, bool, error)
}

type LLM interface {
	RunPrompt(ctx context.Context, prompt string) (string, error)
	RunProactivePrompt(ctx context.Context, prompt string) (string, error)
}

type Session struct {
	UserID int64
	Dirs   userspace.Dirs

	DB *sql.DB

	Registry *tools.Registry

	Subagents SubagentStore
	Confirm   Confirmer
	Secrets   SecretGetter
	LLM       LLM

	Active map[string]bool
}

func NewSession(reg *tools.Registry) *Session {
	active := map[string]bool{}
	// Minimal always-on tools.
	// Note: keep this reasonably small; these are the most commonly needed capabilities.
	active["tool.search"] = true
	active["tool.enable"] = true
	active["confirm.request"] = true
	active["read"] = true
	active["write"] = true
	active["web-search"] = true
	active["web-fetch"] = true
	active["fetch.summarize"] = true
	active["subagent.spawn"] = true
	active["subagent.status"] = true
	return &Session{Registry: reg, Active: active}
}

func (s *Session) IsActive(name string) bool { return s.Active[name] }

func (s *Session) Enable(name string) error {
	// Only allow enabling known tools.
	found := false
	for _, t := range s.Registry.List() {
		if t.Name == name {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("unknown tool: %s", name)
	}
	s.Active[name] = true
	return nil
}
