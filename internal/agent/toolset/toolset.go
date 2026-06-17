package toolset

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

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
	// Spec returns the canonical documentation for this tool (input/output shapes, examples).
	Spec() tools.ToolSpec
	// Definition returns the OpenAI/OpenRouter function-tool definition.
	Definition() ToolDef
	Execute(ctx context.Context, s *Session, rawArgs json.RawMessage) (any, error)
}

type SubagentSkill struct {
	Name      string
	Arguments string
}

type SubagentSpawnRequest struct {
	Prompt       string
	AllowedTools []string
	Skill        *SubagentSkill
}

// Session represents a per-user agent session (active tools, per-user config, etc.).
type SubagentStore interface {
	Spawn(userID int64, req SubagentSpawnRequest) (id string)
	Status(userID int64, id string) (status any, ok bool)
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

type MCPCaller interface {
	CallTool(ctx context.Context, userID int64, serverName string, toolName string, arguments map[string]any) (*mcpsdk.CallToolResult, error)
}

// ToolInvoker dispatches a tool by name against the session, reusing the same
// execution path as direct model tool calls. It is provided by the agent
// runtime (which holds the concrete tool implementations) and is used by the
// `code` tool to let sandboxed scripts call other enabled tools over RPC.
//
// Implementations are responsible for name resolution (LLM-facing vs internal
// names), the active/allowed gate, and refusing to invoke the `code` tool
// recursively.
type ToolInvoker interface {
	Invoke(ctx context.Context, s *Session, name string, rawArgs json.RawMessage) (any, error)
}

type LLM interface {
	RunPrompt(ctx context.Context, prompt string) (string, error)
	RunProactivePrompt(ctx context.Context, prompt string) (string, error)
	RunProactivePromptForUser(ctx context.Context, userID int64, prompt string) (string, error)
}

type InvokedSkill struct {
	Name      string
	Content   string
	InvokedAt int64 // unix seconds; informational
}

type Session struct {
	UserID         int64
	ConversationID int64
	Dirs           userspace.Dirs
	SessionID      string
	StartedAt      time.Time
	LastActivityAt time.Time

	// IsSubagent is set by the runtime when tools are being executed from a sub-agent context.
	// Some tools (e.g. self.schedule) are explicitly disallowed for sub-agents.
	IsSubagent bool

	DB *sql.DB

	Registry *tools.Registry

	Subagents SubagentStore
	Confirm   Confirmer
	Secrets   SecretGetter
	MCP       MCPCaller
	LLM       LLM
	// Tools dispatches other enabled tools on behalf of the `code` tool.
	Tools ToolInvoker

	Active map[string]bool
	// Allowed constrains the total tool universe for this session.
	// Nil means any registered tool may be enabled/used.
	Allowed map[string]bool

	// SkillSessionID is used for ${CLAUDE_SESSION_ID} substitutions.
	SkillSessionID string
	// InvokedSkills are kept in-memory for the lifetime of the Tether process.
	// They are re-attached to the prompt each turn so they don’t fall out of the recent-history window.
	InvokedSkills []InvokedSkill

	TotalToolCalls    int
	TotalInputTokens  int
	TotalOutputTokens int
	TotalTokens       int
	TotalCost         float64
	LastModel         string
	LastInputTokens   int
	LastContextLimit  int

	// ReadPaths tracks files read during this session so write can require prior inspection.
	ReadPaths map[string]bool

	// BashNetworkEnabled allows the bash tool to run with network access for this session.
	// It must only be enabled through an explicit confirmed action.
	BashNetworkEnabled bool

	// AllowPrivateNetworkFetch permits web-fetch to reach private/loopback/
	// link-local addresses. Set from config each turn; default false (SSRF guard).
	AllowPrivateNetworkFetch bool

	// AllowHostExec permits the non-sandboxed host bash tool. Set from config for
	// the main session only (never for sub-agents); default false. Even when true,
	// each host command requires a per-call confirmation and a stated reason.
	AllowHostExec bool
}

// AddInvokedSkill stores/replaces the most recent invocation of a skill.
func (s *Session) AddInvokedSkill(name string, content string) {
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}
	// Remove existing.
	out := make([]InvokedSkill, 0, len(s.InvokedSkills)+1)
	for _, it := range s.InvokedSkills {
		if strings.EqualFold(it.Name, name) {
			continue
		}
		out = append(out, it)
	}
	out = append(out, InvokedSkill{Name: name, Content: content, InvokedAt: time.Now().Unix()})
	s.InvokedSkills = out
}

func NewSession(reg *tools.Registry) *Session {
	active := map[string]bool{}
	// Minimal always-on tools.
	// Note: keep this reasonably small; these are the most commonly needed capabilities.
	active["tool.search"] = true
	active["tool.enable"] = true
	active["tool.describe"] = true
	active["confirm.request"] = true
	active["confirm.scope"] = true
	active["read"] = true
	active["write"] = true
	active["edit"] = true
	active["code"] = true
	active["web-search"] = true
	active["web-fetch"] = true
	active["fetch.summarize"] = true
	active["subagent.spawn"] = true
	active["subagent.status"] = true
	active["skill.invoke"] = true
	now := time.Now().UTC()
	return &Session{
		Registry:       reg,
		Active:         active,
		SessionID:      fmt.Sprintf("sess-%d", now.UnixNano()),
		StartedAt:      now,
		LastActivityAt: now,
		SkillSessionID: fmt.Sprintf("tether-%d", now.UnixNano()),
		ReadPaths:      map[string]bool{},
	}
}

func (s *Session) TouchActivity() {
	if s == nil {
		return
	}
	s.LastActivityAt = time.Now().UTC()
}

func (s *Session) MarkReadPath(relPath string) {
	if s == nil {
		return
	}
	relPath = strings.TrimSpace(relPath)
	if relPath == "" {
		return
	}
	if s.ReadPaths == nil {
		s.ReadPaths = map[string]bool{}
	}
	s.ReadPaths[relPath] = true
}

func (s *Session) HasReadPath(relPath string) bool {
	if s == nil || s.ReadPaths == nil {
		return false
	}
	return s.ReadPaths[strings.TrimSpace(relPath)]
}

func (s *Session) IsActive(name string) bool { return s.Active[name] }

func (s *Session) IsAllowed(name string) bool {
	if s == nil {
		return false
	}
	if s.Allowed == nil {
		return true
	}
	return s.Allowed[name]
}

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
	if !s.IsAllowed(name) {
		return fmt.Errorf("tool not allowed in this session: %s", name)
	}
	s.Active[name] = true
	return nil
}
