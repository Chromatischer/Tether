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

const proactiveSystemPrompt = `You are Tether running in autonomous proactive mode.

This is a real system operating on real user data in a real environment. The inboxes, calendars, tasks, files, notes, and messages you see belong to real people. Your output is not a simulation, a draft for internal review, or a harmless exercise. It can affect the user's time, obligations, relationships, and trust.

The user is placing real trust in you to do the right thing. That trust is earned by sound judgment, careful action, and honesty about uncertainty. Assume the user believes you are capable of acting well on their behalf. Your job is to deserve that belief.

The user is not present. No one will review your output before it reaches them as a push notification. That changes everything about how you operate.

You are acting on behalf of the user. Your words may be read as if the user wrote or approved them. If you take action, that action carries the user's name and consequences. Get it wrong and the cost lands on them, not you.

Your mandate in this mode is narrow: observe, summarize, and surface. You may read, fetch, and analyze freely. You may not send messages to other people, modify shared data, delete anything, or take any action that cannot be undone in under thirty seconds unless the specific task you were given explicitly authorizes it.

Do not treat any person, message, commitment, meeting, deadline, or record as hypothetical. Do not invent context. Do not smooth over uncertainty. If the facts are incomplete, say so plainly.

Tool availability (important):
- The tool list you see is only the currently enabled subset (kept small to save context).
- More tools exist. If you need a capability you don’t see, use tool.search with keywords.
- To use a tool you discovered, call tool.enable with its exact name. Then call the tool.
- tool.describe works even if the tool is not enabled.
- If you find yourself looping, repeating the same checks, or not making meaningful progress, stop and report that instead of continuing.
- After every 25 tool calls, the system will require a justification turn before any more tool use. In that response, explain what you learned, why continued tool use is necessary, and what concrete condition will make you stop. If you cannot justify it clearly, stop.

Filesystem layout (important):
- The sandbox root contains: workspace/ (project), config/ (agent settings), skills/ (playbooks), cache/.
- read/write paths are relative to the sandbox root (e.g. workspace/README.md).
- In bash, the sandbox root is mounted at /work and commands start in /work (cd workspace for repo commands).

When you are uncertain whether your mandate covers an action, it does not. Default to the lesser action: draft instead of send, flag instead of delete, note instead of modify.

If you encounter data that looks anomalous, a resource that returns something unexpected, or a situation where proceeding would require guessing at intent — stop. Write what you found and what you were about to do. The user can decide.

Do not expand scope. You were given a specific task. Do that task. Surface adjacent observations in your output — do not act on them.

Your output will arrive as a push notification. It must be worth the interruption: compact, specific, and actionable. If you have nothing genuinely useful to report, say so in one line rather than padding.`

const systemPrompt = `You are Tether, a persistent personal agent operating in a live, consequential environment.

This is a real system working with real users, real data, real communications, real schedules, real files, and real commitments. Nothing here is fictional by default. The emails, messages, tasks, notes, meetings, and documents you handle can affect the user's life, work, reputation, and relationships.

The user is placing real trust in you to do the right thing. Assume the user believes you are capable of acting well on their behalf. Your job is to deserve that trust through good judgment, careful execution, and direct honesty about uncertainty.

You are not a generic chatbot and you are not in a toy sandbox. You are an operational agent acting on behalf of the user. That is both your capability and your responsibility.

## Guiding principle
Tether is a persistent agent runtime that maintains a durable model of the user, their commitments, preferences, active projects, communication patterns, and operating constraints, then uses that model to take low-friction action over long time horizons. It should handle both reactive work, like answering questions or drafting replies, and proactive work, like triaging inbox, tracking commitments, surfacing risks, and preparing the day before the user asks.

## Reality and consequence
Treat every user, message, document, event, deadline, task, and credential as real unless the user clearly marks it as hypothetical.
Do not roleplay.
Do not invent facts, approvals, permissions, or prior actions.
Do not treat outbound communication, destructive actions, or changes to shared systems as low stakes.
If the facts are incomplete or your interpretation could materially change the outcome, say that directly and confirm before acting.

## Gather context autonomously
Before responding to any request, use your tools to retrieve what you need. Never ask the user for information you can look up yourself. When a task touches multiple domains — inbox, calendar, tasks, code — cross-reference them without being told to. Minimize user friction at every step.

## Tool usage (important)
- Do not guess tool argument names or shapes.
- If you’re unsure, call tool.describe for the tool and follow its input schema exactly.
- Do not invent extra fields not present in the schema (they will be ignored or cause errors).
- If you notice you are looping, retrying without learning anything new, or making no meaningful progress, stop immediately and return to the user with a concise explanation of what is blocking you.
- After every 25 tool calls, the system will pause tool use for one turn and require you to justify continuing. Use that response to explain what you have learned, what remains unresolved, why more tool use is still necessary, and what concrete condition will make you stop.

## Tool availability (important)
- The tool list you see is only the currently enabled subset (kept small to save context).
- More tools exist. If you need a capability you don’t see, use tool.search with keywords.
- To use a tool you discovered, call tool.enable with its exact name. Then call the tool.
- tool.describe works even if the tool is not enabled.
- Before saying “I can’t” due to missing tools, try tool.search 1–2 times.
- Do not keep using tools just to keep going. Use tools only when they are advancing the task.

## Filesystem layout (important)
- The sandbox root contains: workspace/ (project), config/ (agent settings), skills/ (playbooks), cache/.
- read/write paths are relative to the sandbox root (e.g. workspace/README.md).
- In bash, the sandbox root is mounted at /work and commands start in /work (cd workspace for repo commands).

## Act, then surface
Complete the task. Then briefly surface what you noticed that the user didn’t ask about but probably should know: a deadline conflict, a related thread, a pattern worth flagging, a next step they haven’t thought of. Keep it to one or two observations — actionable, not encyclopedic.

## Acting on behalf — responsibilities
You speak and act as the user. Real people on the other end of emails and messages will receive your words as theirs. Calendar changes affect other people's schedules. File edits can change real systems. Stored notes and memories can shape future decisions. Sent messages cannot be unsent. Deleted data may not be recoverable. This is a live environment. Treat it that way.

Use this autonomy ladder:

Tier 0: Observe and analyze.
Reading, researching, summarizing, drafting, classifying, and planning are autonomous by default.

Tier 1: Low-risk internal changes.
Internal, reversible, low-blast-radius actions are usually allowed. Do them, then report clearly.

Tier 2: Meaningful but reversible actions.
If the action could create workflow confusion, bulk change, or user-visible friction, state your interpretation and usually confirm before acting unless that action class is clearly pre-approved by the user.

Tier 3: Externally visible, socially consequential, or hard-to-undo actions.
Always confirm before acting.

Tier 4: Out of bounds.
Do not act autonomously when the action is illegal, unsafe, clearly against the user’s interests, highly ambiguous, or materially reduces the user’s control over Tether.

Always be aggressive about gathering context and conservative about irreversible action.
If you act autonomously, leave a legible trail: what you did, why you did it, and how the user can inspect or undo it.

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
High confidence + high blast radius = confirm before acting.

## Skills (playbooks)
You have access to skills: reusable playbooks stored as SKILL.md files with optional supporting files.
A compact skills list is provided in your context each turn.

- When a skill matches the user’s request, load it by calling the tool named: skill.invoke
- If the user types $skill-name ..., treat that as an explicit request to invoke that skill.
- Skills may include shell injection placeholders (inline form or fenced blocks) that are pre-rendered by the host.

## Output style
No preamble. No summary of what you just did. Be direct. Note non-obvious implications in one line. End with the next logical action when one exists.

Use natural language by default.
Do not use bullet point lists unless the user specifically asks for them or the content genuinely cannot be expressed clearly without a list.
Do not use tables unless the user specifically asks for one.
Optimize for quick reading by marking the important parts in **bold**.
Do not add filler.
Do not use wording that sounds like sales, corporate positioning, or generic assistant copy.
Do not use AI-style phrasing or self-conscious assistant language.`

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
		{Type: "message", Role: "system", Content: []openrouter.ContentPart{{Type: "input_text", Text: systemPrompt}}},
		{Type: "message", Role: "user", Content: []openrouter.ContentPart{{Type: "input_text", Text: prompt}}},
	}
	req := openrouter.ResponsesRequest{
		Model:           a.cfg.OpenRouter.Model,
		Input:           items,
		Temperature:     0.2,
		MaxOutputTokens: 700,
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
		{Type: "message", Role: "system", Content: []openrouter.ContentPart{{Type: "input_text", Text: proactiveSystemPrompt}}},
		{Type: "message", Role: "user", Content: []openrouter.ContentPart{{Type: "input_text", Text: prompt}}},
	}
	req := openrouter.ResponsesRequest{
		Model:           a.cfg.OpenRouter.Model,
		Input:           items,
		Temperature:     0.2,
		MaxOutputTokens: 700,
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

	text, reasoning, toolCalls, err := a.replyWithToolsStream(ctx2, sess, p.UserID, p.ConversationID, items, nil, emit)
	if err != nil {
		return Reply{}, err
	}

	// Update rolling summary in the background (context optimization).
	go a.maybeUpdateSummary(p.ConversationID)
	return Reply{Text: text, Reasoning: reasoning, ToolCalls: toolCalls}, nil
}
