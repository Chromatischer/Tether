package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

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

type Reply struct {
	Text string
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
- If the user types /skill-name ..., treat that as an explicit request to invoke that skill.
- Skills may include shell injection placeholders (inline form or fenced blocks) that are pre-rendered by the host.

## Output style
No preamble. No summary of what you just did. Be direct. Note non-obvious implications in one line. End with the next logical action when one exists.`

func (a *Agent) chatCached(ctx context.Context, req openrouter.ChatRequest) (openrouter.ChatResponse, error) {
	payload, _ := json.Marshal(req)
	key := cache.KeyFromBytes(payload)
	if cached, ok, err := a.cache.Get(key); err == nil && ok {
		var resp openrouter.ChatResponse
		if err := json.Unmarshal([]byte(cached), &resp); err == nil {
			return resp, nil
		}
		// Cache corruption; ignore.
	}

	resp, err := a.llm.Chat(ctx, req)
	if err != nil {
		return openrouter.ChatResponse{}, err
	}
	b, _ := json.Marshal(resp)
	_ = a.cache.Put(key, string(b))
	return resp, nil
}

func (a *Agent) RunPrompt(ctx context.Context, prompt string) (string, error) {
	if strings.TrimSpace(a.cfg.OpenRouter.APIKey) == "" {
		return "", errors.New("OPENROUTER_API_KEY not configured")
	}
	msgs := []openrouter.Message{
		{Role: "system", Content: openrouter.Text(systemPrompt)},
		{Role: "user", Content: openrouter.Text(prompt)},
	}
	req := openrouter.ChatRequest{
		Model:       a.cfg.OpenRouter.Model,
		Messages:    msgs,
		Temperature: 0.2,
		MaxTokens:   700,
	}
	resp, err := a.chatCached(ctx, req)
	if err != nil {
		return "", fmt.Errorf("llm: %w", err)
	}
	text := ""
	if resp.Choices[0].Message.Content != nil {
		text = *resp.Choices[0].Message.Content
	}
	return strings.TrimSpace(text), nil
}

// RunPromptForUser is a convenience wrapper used by subsystems (e.g. subagents)
// that only have a user_id + a standalone prompt, but still want to respect the
// user's personality.
func (a *Agent) RunPromptForUser(ctx context.Context, userID int64, prompt string) (string, error) {
	if strings.TrimSpace(a.cfg.OpenRouter.APIKey) == "" {
		return "", errors.New("OPENROUTER_API_KEY not configured")
	}
	p := a.personalityText(userID, personality.AgentChat)
	msgs := []openrouter.Message{{Role: "system", Content: openrouter.Text(systemPrompt)}}
	if strings.TrimSpace(p) != "" {
		msgs = append(msgs, openrouter.Message{Role: "system", Content: openrouter.Text("Agent personality:\n" + p)})
	}
	msgs = append(msgs, openrouter.Message{Role: "user", Content: openrouter.Text(prompt)})
	req := openrouter.ChatRequest{
		Model:       a.cfg.OpenRouter.Model,
		Messages:    msgs,
		Temperature: 0.2,
		MaxTokens:   700,
	}
	resp, err := a.chatCached(ctx, req)
	if err != nil {
		return "", fmt.Errorf("llm: %w", err)
	}
	text := ""
	if resp.Choices[0].Message.Content != nil {
		text = *resp.Choices[0].Message.Content
	}
	return strings.TrimSpace(text), nil
}

func (a *Agent) RunProactivePrompt(ctx context.Context, prompt string) (string, error) {
	if strings.TrimSpace(a.cfg.OpenRouter.APIKey) == "" {
		return "", errors.New("OPENROUTER_API_KEY not configured")
	}
	msgs := []openrouter.Message{
		{Role: "system", Content: openrouter.Text(proactiveSystemPrompt)},
		{Role: "user", Content: openrouter.Text(prompt)},
	}
	req := openrouter.ChatRequest{
		Model:       a.cfg.OpenRouter.Model,
		Messages:    msgs,
		Temperature: 0.2,
		MaxTokens:   700,
	}
	resp, err := a.chatCached(ctx, req)
	if err != nil {
		return "", fmt.Errorf("llm: %w", err)
	}
	text := ""
	if resp.Choices[0].Message.Content != nil {
		text = *resp.Choices[0].Message.Content
	}
	return strings.TrimSpace(text), nil
}

func (a *Agent) Reply(ctx context.Context, p ReplyParams) (Reply, error) {
	if strings.TrimSpace(a.cfg.OpenRouter.APIKey) == "" {
		return Reply{}, errors.New("OPENROUTER_API_KEY not configured")
	}

	history, err := store.ListRecentMessages(a.db, p.ConversationID, 25)
	if err != nil {
		return Reply{}, err
	}

	msgs, err := a.buildContextMessages(p.UserID, p.ConversationID, history)
	if err != nil {
		return Reply{}, err
	}

	text, err := a.replyWithTools(ctx, p.UserID, p.ConversationID, msgs)
	if err != nil {
		return Reply{}, err
	}
	// Update rolling summary in the background (context optimization).
	go a.maybeUpdateSummary(p.ConversationID)
	return Reply{Text: text}, nil
}
