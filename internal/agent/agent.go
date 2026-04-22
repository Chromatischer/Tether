package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"charm.land/log/v2"

	"tether/internal/agent/toolset"
	"tether/internal/cache"
	"tether/internal/config"
	"tether/internal/llm/openrouter"
	"tether/internal/mcp"
	"tether/internal/personality"
	"tether/internal/secrets"
	"tether/internal/subagents"
	"tether/internal/tools"
)

type ReplyParams struct {
	UserID         int64
	ConversationID int64
	Text           string
}

// ToolCallInfo records one tool invocation for display in the chat UI.
type ToolCallInfo struct {
	Name   string
	Args   string // truncated JSON args, may be empty
	Result string // truncated result preview, may be empty
}

type Reply struct {
	Text      string
	Reasoning string
	ToolCalls []ToolCallInfo
}

type pendingConfirmation struct {
	UserID         int64
	ConversationID int64
	Token          string
	Scope          string
	Session        *toolset.Session
	Items          []openrouter.ResponseItem
	Call           openrouter.ResponseItem
	ToolCalls      []ToolCallInfo
	CreatedAt      time.Time
}

type Agent struct {
	cfg   *config.Config
	db    *sql.DB
	llm   *openrouter.Client
	cache *cache.LLMCache

	registry *tools.Registry
	toolImpl map[string]toolset.Tool

	subMgr   *subagents.Manager
	subStore toolset.SubagentStore

	confirm *confirmManager
	secrets *secrets.Store
	mcp     *mcp.Manager

	modelInfoCache map[string]modelInfo

	mu          sync.Mutex
	sessions    map[int64]*toolset.Session      // conversation_id -> session
	pendingRuns map[string]*pendingConfirmation // confirm token -> suspended tool execution
}

func New(cfg *config.Config, db *sql.DB) *Agent {
	return newAgent(cfg, db)
}

func (a *Agent) ReloadRuntimeConfig() {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.llm != nil {
		a.llm.BaseURL = a.cfg.OpenRouter.BaseURL
		a.llm.APIKey = a.cfg.OpenRouter.APIKey
	}

	if strings.TrimSpace(a.cfg.Secrets.MasterKey) == "" {
		a.secrets = nil
		return
	}

	st, err := secrets.NewStore(a.db, a.cfg.Secrets.MasterKey, time.Duration(a.cfg.Secrets.TTLHours)*time.Hour)
	if err != nil {
		a.secrets = nil
		return
	}
	a.secrets = st
}

func (a *Agent) responsesCached(ctx context.Context, req openrouter.ResponsesRequest) (openrouter.ResponsesResponse, error) {
	payload, _ := json.Marshal(req)
	key := cache.KeyFromBytes(payload)
	if cached, ok, err := a.cache.Get(key); err == nil && ok {
		var resp openrouter.ResponsesResponse
		if err := json.Unmarshal([]byte(cached), &resp); err == nil {
			return resp, nil
		}
		// Cache corruption; ignore.
	}

	resp, err := a.llm.Responses(ctx, req)
	if err != nil {
		// Always log LLM request failures; otherwise they can be easy to miss if only surfaced to UI.
		a.logLLMError("openrouter.responses", req.Model, 0, 0, err)
		return openrouter.ResponsesResponse{}, err
	}
	b, _ := json.Marshal(resp)
	_ = a.cache.Put(key, string(b))
	return resp, nil
}

func withDefaultTimeout(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, d)
}

func (a *Agent) openRouterProviderPrefs() *openrouter.ProviderPreferences {
	p := openrouter.ProviderPreferences{}
	p.AllowFallbacks = a.cfg.OpenRouter.Provider.AllowFallbacks

	if len(a.cfg.OpenRouter.Provider.Ignore) > 0 {
		p.Ignore = append([]string{}, a.cfg.OpenRouter.Provider.Ignore...)
	}
	if len(a.cfg.OpenRouter.Provider.Only) > 0 {
		p.Only = append([]string{}, a.cfg.OpenRouter.Provider.Only...)
	}
	if len(a.cfg.OpenRouter.Provider.Order) > 0 {
		p.Order = append([]string{}, a.cfg.OpenRouter.Provider.Order...)
	}

	if p.AllowFallbacks == nil && len(p.Ignore) == 0 && len(p.Only) == 0 && len(p.Order) == 0 {
		return nil
	}
	return &p
}

func (a *Agent) logLLMError(op string, model string, userID, convID int64, err error) {
	if err == nil {
		return
	}
	fields := []any{"op", op, "model", model}
	if userID != 0 {
		fields = append(fields, "user_id", userID)
	}
	if convID != 0 {
		fields = append(fields, "conversation_id", convID)
	}

	var herr *openrouter.HTTPError
	if errors.As(err, &herr) {
		fields = append(fields,
			"status", herr.StatusCode,
			"message", herr.Message(),
			"provider", herr.ProviderName(),
		)
		raw := herr.RawUpstreamError()
		if raw != "" {
			if len(raw) > 800 {
				raw = raw[:800] + "…"
			}
			fields = append(fields, "upstream", raw)
		}
		log.Warn("llm request failed", append(fields, "error", err)...)
		return
	}

	log.Warn("llm request failed", append(fields, "error", err)...)
}

func extractResponsesText(resp openrouter.ResponsesResponse) string {
	var b strings.Builder
	for _, it := range resp.Output {
		if it.Type != "message" || it.Role != "assistant" {
			continue
		}
		for _, p := range it.Content {
			switch p.Type {
			case "output_text", "input_text":
				b.WriteString(p.Text)
			}
		}
	}
	return b.String()
}

func extractResponsesReasoning(resp openrouter.ResponsesResponse) string {
	parts := make([]string, 0, len(resp.Output))
	for _, it := range resp.Output {
		if it.Type != "reasoning" || len(it.Summary) == 0 {
			continue
		}
		lines := make([]string, 0, len(it.Summary))
		for _, part := range it.Summary {
			if text := strings.TrimSpace(part.Text); text != "" {
				lines = append(lines, text)
			}
		}
		if len(lines) > 0 {
			parts = append(parts, strings.Join(lines, "\n"))
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n\n"))
}

func (a *Agent) RunPrompt(ctx context.Context, prompt string) (string, error) {
	if strings.TrimSpace(a.cfg.OpenRouter.APIKey) == "" {
		return "", errors.New("OPENROUTER_API_KEY not configured")
	}
	ctx2, cancel := withDefaultTimeout(ctx, 60*time.Second)
	defer cancel()

	items := []openrouter.ResponseItem{
		{Type: "message", Role: "system", Content: []openrouter.ContentPart{{Type: "input_text", Text: a.defaultChatSystemPromptText()}}},
		{Type: "message", Role: "user", Content: []openrouter.ContentPart{{Type: "input_text", Text: prompt}}},
	}
		req := openrouter.ResponsesRequest{
			Model:           a.cfg.OpenRouter.Model,
			Input:           items,
			Temperature:     0.2,
			ToolChoice:      "none",
			Provider:        a.openRouterProviderPrefs(),
		}
	resp, err := a.responsesCached(ctx2, req)
	if err != nil {
		return "", fmt.Errorf("llm: %w", err)
	}
	return strings.TrimSpace(extractResponsesText(resp)), nil
}

// RunPromptForUser is a convenience wrapper used by subsystems (e.g. subagents)
// that only have a user_id + a standalone prompt, but still want to respect the
// user's personality.
func (a *Agent) RunPromptForUser(ctx context.Context, userID int64, prompt string) (string, error) {
	if strings.TrimSpace(a.cfg.OpenRouter.APIKey) == "" {
		return "", errors.New("OPENROUTER_API_KEY not configured")
	}
	ctx2, cancel := withDefaultTimeout(ctx, 60*time.Second)
	defer cancel()

	p := a.personalityText(userID, personality.AgentChat)
	items := []openrouter.ResponseItem{
		{Type: "message", Role: "system", Content: []openrouter.ContentPart{{Type: "input_text", Text: a.chatSystemPromptText(userID, 0)}}},
	}
	if strings.TrimSpace(p) != "" {
		items = append(items, openrouter.ResponseItem{Type: "message", Role: "system", Content: []openrouter.ContentPart{{Type: "input_text", Text: "Agent personality:\n" + p}}})
	}
	items = append(items, openrouter.ResponseItem{Type: "message", Role: "user", Content: []openrouter.ContentPart{{Type: "input_text", Text: prompt}}})

		req := openrouter.ResponsesRequest{
			Model:           a.cfg.OpenRouter.Model,
			Input:           items,
			Temperature:     0.2,
			ToolChoice:      "none",
			Provider:        a.openRouterProviderPrefs(),
		}
	resp, err := a.responsesCached(ctx2, req)
	if err != nil {
		return "", fmt.Errorf("llm: %w", err)
	}
	return strings.TrimSpace(extractResponsesText(resp)), nil
}

func (a *Agent) RunProactivePrompt(ctx context.Context, prompt string) (string, error) {
	if strings.TrimSpace(a.cfg.OpenRouter.APIKey) == "" {
		return "", errors.New("OPENROUTER_API_KEY not configured")
	}
	ctx2, cancel := withDefaultTimeout(ctx, 60*time.Second)
	defer cancel()

	items := []openrouter.ResponseItem{
		{Type: "message", Role: "system", Content: []openrouter.ContentPart{{Type: "input_text", Text: a.defaultProactiveSystemPromptText()}}},
		{Type: "message", Role: "user", Content: []openrouter.ContentPart{{Type: "input_text", Text: prompt}}},
	}
		req := openrouter.ResponsesRequest{
			Model:           a.cfg.OpenRouter.Model,
			Input:           items,
			Temperature:     0.2,
			ToolChoice:      "none",
			Provider:        a.openRouterProviderPrefs(),
		}
	resp, err := a.responsesCached(ctx2, req)
	if err != nil {
		return "", fmt.Errorf("llm: %w", err)
	}
	return strings.TrimSpace(extractResponsesText(resp)), nil
}

func (a *Agent) RunProactivePromptForUser(ctx context.Context, userID int64, prompt string) (string, error) {
	if strings.TrimSpace(a.cfg.OpenRouter.APIKey) == "" {
		return "", errors.New("OPENROUTER_API_KEY not configured")
	}
	ctx2, cancel := withDefaultTimeout(ctx, 60*time.Second)
	defer cancel()

	items := []openrouter.ResponseItem{
		{Type: "message", Role: "system", Content: []openrouter.ContentPart{{Type: "input_text", Text: a.proactiveSystemPromptText(userID, 0)}}},
		{Type: "message", Role: "user", Content: []openrouter.ContentPart{{Type: "input_text", Text: prompt}}},
	}
		req := openrouter.ResponsesRequest{
			Model:           a.cfg.OpenRouter.Model,
			Input:           items,
			Temperature:     0.2,
			ToolChoice:      "none",
			Provider:        a.openRouterProviderPrefs(),
		}
	resp, err := a.responsesCached(ctx2, req)
	if err != nil {
		return "", fmt.Errorf("llm: %w", err)
	}
	return strings.TrimSpace(extractResponsesText(resp)), nil
}

func (a *Agent) Reply(ctx context.Context, p ReplyParams) (Reply, error) {
	return a.ReplyStream(ctx, p, nil)
}

// ReplyStream is like Reply, but optionally emits incremental streaming events.
// The returned Reply is the final assistant text + the list of tool calls invoked.
func (a *Agent) ReplyStream(ctx context.Context, p ReplyParams, emit func(StreamEvent)) (Reply, error) {
	if strings.TrimSpace(a.cfg.OpenRouter.APIKey) == "" {
		return Reply{}, errors.New("OPENROUTER_API_KEY not configured")
	}
	if sess := a.sessionFor(p.UserID, p.ConversationID); sess != nil {
		sess.TouchActivity()
	}

	sess := a.forkSessionFor(p.UserID, p.ConversationID)
	defer a.mergeSessionFor(p.ConversationID, sess)

	items, err := a.buildContextInputItemsWithSession(ctx, sess, p.UserID, p.ConversationID, nil)
	if err != nil {
		return Reply{}, err
	}

	text, reasoning, toolCalls, err := a.replyWithToolsStream(ctx, sess, p.UserID, p.ConversationID, items, nil, emit)
	if err != nil {
		return Reply{}, err
	}

	// Update rolling summary in the background (context optimization).
	go a.maybeUpdateSummary(p.ConversationID)
	return Reply{Text: text, Reasoning: reasoning, ToolCalls: toolCalls}, nil
}
