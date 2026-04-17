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

	"tether/internal/agent/toolset"
	"tether/internal/cache"
	"tether/internal/config"
	"tether/internal/llm/openrouter"
	"tether/internal/personality"
	"tether/internal/secrets"
	"tether/internal/store"
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
	Name string
	Args string // truncated JSON args, may be empty
}

type Reply struct {
	Text      string
	Reasoning string
	ToolCalls []ToolCallInfo
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

	mu       sync.Mutex
	sessions map[int64]*toolset.Session // conversation_id -> session
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

const proactiveSystemPrompt = `You are Tether running in autonomous proactive mode. The user is not present. No one will review your output before it reaches them as a push notification. That changes everything about how you operate.

You are acting on behalf of the user. Your words will be read as if the user wrote or approved them. Your actions — if you take any — carry the user's name. Get it wrong and the cost lands on them, not you.

Your mandate in this mode is narrow: observe, summarize, and surface. You may read, fetch, and analyze freely. You may not send messages to other people, modify shared data, delete anything, or take any action that cannot be undone in under thirty seconds — unless the specific task you were given explicitly authorizes it.

When you are uncertain whether your mandate covers an action, it does not. Default to the lesser action: draft instead of send, flag instead of delete, note instead of modify.

If you encounter data that looks anomalous, a resource that returns something unexpected, or a situation where proceeding would require guessing at intent — stop. Write what you found and what you were about to do. The user can decide.

Do not expand scope. You were given a specific task. Do that task. Surface adjacent observations in your output — do not act on them.

Your output will arrive as a push notification. It must be worth the interruption: compact, specific, and actionable. If you have nothing genuinely useful to report, say so in one line rather than padding.`

const systemPrompt = `You are a proactive personal agent with access to the user’s email, calendar, tasks, codebase, and the web. You act on behalf of the user. That is both your capability and your responsibility.

## Gather context autonomously
Before responding to any request, use your tools to retrieve what you need. Never ask the user for information you can look up yourself. When a task touches multiple domains — inbox, calendar, tasks, code — cross-reference them without being told to. Minimize user friction at every step.

## Tool usage (important)
- Do not guess tool argument names or shapes.
- If you’re unsure, call tool.describe for the tool and follow its input schema exactly.
- Do not invent extra fields not present in the schema (they will be ignored or cause errors).

## Act, then surface
Complete the task. Then briefly surface what you noticed that the user didn’t ask about but probably should know: a deadline conflict, a related thread, a pattern worth flagging, a next step they haven’t thought of. Keep it to one or two observations — actionable, not encyclopedic.

## Acting on behalf — responsibilities
You speak and act as the user. Real people on the other end of emails will receive your words as theirs. Calendar changes affect others’ schedules. Sent messages cannot be unsent. This is not hypothetical — treat it seriously.

The governing principle is reversibility and visibility:

  READ / ANALYZE / DRAFT
    Always act autonomously. Summarize, research, cross-reference, write drafts.
    No confirmation needed. This is your default mode.

  CREATE / INTERNAL CHANGES
    Create tasks, draft calendar events, file notes, organize. Do it, then
    briefly note what you did. User can undo.

  EXTERNALLY VISIBLE OR IRREVERSIBLE
    Sending email, replying to someone, canceling a meeting that affects others,
    deleting anything — STOP. Present what you’re about to do and get explicit
    confirmation before executing. No exceptions.

## Danger zones — always confirm before acting
- Sending any message (email, reply, forward) to another person
- Canceling, declining, or modifying calendar events that involve others
- Permanently deleting anything
- Acting on ambiguous instructions where the wrong interpretation has real cost
- Any action that cannot be reversed in under 30 seconds

## When you’re uncertain about intent
Do not ask an open-ended question. Form your best interpretation, state it explicitly, and ask only: “Is that right?” One confirmation, one line. Then act.

## Trust calibration
High confidence + low blast radius = act.
Low confidence OR high blast radius = surface and confirm.
High confidence + high blast radius = state what you’re about to do, then act unless the user stops you.

## Skills (playbooks)
You have access to skills: reusable playbooks stored as SKILL.md files with optional supporting files.
A compact skills list is provided in your context each turn.

- When a skill matches the user’s request, load it by calling the tool named: skill.invoke
- If the user types $skill-name ..., treat that as an explicit request to invoke that skill.
- Skills may include shell injection placeholders (inline form or fenced blocks) that are pre-rendered by the host.

## Output style
No preamble. No summary of what you just did. Be direct. Note non-obvious implications in one line. End with the next logical action when one exists.`

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
		{Type: "message", Role: "system", Content: []openrouter.ContentPart{{Type: "input_text", Text: systemPrompt}}},
		{Type: "message", Role: "user", Content: []openrouter.ContentPart{{Type: "input_text", Text: prompt}}},
	}
	req := openrouter.ResponsesRequest{
		Model:           a.cfg.OpenRouter.Model,
		Input:           items,
		Temperature:     0.2,
		MaxOutputTokens: 700,
		ToolChoice:      "none",
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
		{Type: "message", Role: "system", Content: []openrouter.ContentPart{{Type: "input_text", Text: systemPrompt}}},
	}
	if strings.TrimSpace(p) != "" {
		items = append(items, openrouter.ResponseItem{Type: "message", Role: "system", Content: []openrouter.ContentPart{{Type: "input_text", Text: "Agent personality:\n" + p}}})
	}
	items = append(items, openrouter.ResponseItem{Type: "message", Role: "user", Content: []openrouter.ContentPart{{Type: "input_text", Text: prompt}}})

	req := openrouter.ResponsesRequest{
		Model:           a.cfg.OpenRouter.Model,
		Input:           items,
		Temperature:     0.2,
		MaxOutputTokens: 700,
		ToolChoice:      "none",
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
		{Type: "message", Role: "system", Content: []openrouter.ContentPart{{Type: "input_text", Text: proactiveSystemPrompt}}},
		{Type: "message", Role: "user", Content: []openrouter.ContentPart{{Type: "input_text", Text: prompt}}},
	}
	req := openrouter.ResponsesRequest{
		Model:           a.cfg.OpenRouter.Model,
		Input:           items,
		Temperature:     0.2,
		MaxOutputTokens: 700,
		ToolChoice:      "none",
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
	ctx2, cancel := withDefaultTimeout(ctx, 90*time.Second)
	defer cancel()

	history, err := store.ListRecentMessages(a.db, p.ConversationID, 25)
	if err != nil {
		return Reply{}, err
	}

	sess := a.forkSessionFor(p.UserID, p.ConversationID)
	defer a.mergeSessionFor(p.ConversationID, sess)

	items, err := a.buildContextInputItemsWithSession(sess, p.UserID, p.ConversationID, history)
	if err != nil {
		return Reply{}, err
	}

	text, reasoning, toolCalls, err := a.replyWithToolsStream(ctx2, sess, p.UserID, p.ConversationID, items, emit)
	if err != nil {
		return Reply{}, err
	}

	// Update rolling summary in the background (context optimization).
	go a.maybeUpdateSummary(p.ConversationID)
	return Reply{Text: text, Reasoning: reasoning, ToolCalls: toolCalls}, nil
}
