package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"tether/internal/agent/toolset"
	"tether/internal/llm/openrouter"
	"tether/internal/store"
)

func (a *Agent) activeTools(s *toolset.Session) []openrouter.Tool {
	defs := make([]toolset.ToolDef, 0, len(s.Active))
	for name := range s.Active {
		impl := a.toolImpl[name]
		if impl == nil {
			continue
		}
		defs = append(defs, impl.Definition())
	}
	sort.Slice(defs, func(i, j int) bool { return defs[i].Name < defs[j].Name })
	out := make([]openrouter.Tool, 0, len(defs))
	for _, d := range defs {
		out = append(out, openrouter.Tool{
			Type: "function",
			Function: openrouter.ToolFunction{
				Name:        d.Name,
				Description: d.Description,
				Parameters:  d.Parameters,
			},
		})
	}
	return out
}

func (a *Agent) executeToolCalls(ctx context.Context, s *toolset.Session, calls []openrouter.ToolCall) []openrouter.Message {
	msgs := make([]openrouter.Message, 0, len(calls))
	for _, c := range calls {
		name := c.Function.Name
		impl := a.toolImpl[name]

		rawArgs := json.RawMessage(strings.TrimSpace(c.Function.Arguments))
		if len(rawArgs) == 0 {
			rawArgs = json.RawMessage(`{}`)
		}

		var execErr error
		var result any
		if impl == nil {
			execErr = fmt.Errorf("unknown tool: %s", name)
			result = map[string]any{"error": execErr.Error()}
		} else if !s.IsActive(name) {
			execErr = fmt.Errorf("tool not enabled: %s", name)
			result = map[string]any{"error": execErr.Error()}
		} else {
			v, err := impl.Execute(ctx, s, rawArgs)
			execErr = err
			if err != nil {
				result = map[string]any{"error": err.Error()}
			} else {
				result = v
			}
		}

		b, _ := json.Marshal(result)
		// Audit tool call (best-effort). Avoid storing raw args/results.
		if s.DB != nil {
			uid := s.UserID
			sum := sha256.Sum256(rawArgs)
			payload := map[string]any{
				"tool":           name,
				"tool_call_id":   c.ID,
				"args_sha256":    hex.EncodeToString(sum[:]),
				"ok":             execErr == nil,
				"error":          truncateAuditErr(execErr),
				"active_tools_n": len(s.Active),
			}
			pb, _ := json.Marshal(payload)
			_ = store.AddAuditEvent(s.DB, &uid, "tool_call", string(pb))
		}

		msgs = append(msgs, openrouter.Message{
			Role:       "tool",
			ToolCallID: c.ID,
			Content:    openrouter.Text(string(b)),
		})
	}
	return msgs
}

func (a *Agent) replyWithTools(ctx context.Context, userID, convID int64, baseMessages []openrouter.Message) (string, []ToolCallInfo, error) {
	s := a.sessionFor(userID, convID)

	messages := append([]openrouter.Message{}, baseMessages...)
	const maxIterations = 8
	var toolCalls []ToolCallInfo

	for i := 0; i < maxIterations; i++ {
		req := openrouter.ChatRequest{
			Model:             a.cfg.OpenRouter.Model,
			Messages:          messages,
			Temperature:       0.2,
			MaxTokens:         700,
			Tools:             a.activeTools(s),
			ParallelToolCalls: false,
			ToolChoice:        "auto",
		}

		resp, err := a.chatCached(ctx, req)
		if err != nil {
			return "", toolCalls, fmt.Errorf("llm: %w", err)
		}

		// Best-effort: record usage + cache stats so we can verify prompt caching is
		// actually saving money on the configured model/provider.
		if s != nil && s.DB != nil && resp.Usage != nil {
			cached := 0
			cacheWrite := 0
			if resp.Usage.PromptTokensDetails != nil {
				cached = resp.Usage.PromptTokensDetails.CachedTokens
				cacheWrite = resp.Usage.PromptTokensDetails.CacheWriteTokens
			}
			payload := map[string]any{
				"model":              req.Model,
				"conversation_id":    convID,
				"iteration":          i,
				"prompt_tokens":      resp.Usage.PromptTokens,
				"completion_tokens":  resp.Usage.CompletionTokens,
				"total_tokens":       resp.Usage.TotalTokens,
				"cached_tokens":      cached,
				"cache_write_tokens": cacheWrite,
				"cost":               resp.Usage.Cost,
				"tools_n":            len(req.Tools),
			}
			pb, _ := json.Marshal(payload)
			uid := userID
			_ = store.AddAuditEvent(s.DB, &uid, "llm_usage", string(pb))
		}

		m := resp.Choices[0].Message

		if len(m.ToolCalls) == 0 {
			text := ""
			if m.Content != nil {
				text = *m.Content
			}
			return strings.TrimSpace(text), toolCalls, nil
		}

		// Collect tool call info for display in the chat UI.
		for _, tc := range m.ToolCalls {
			args := strings.TrimSpace(tc.Function.Arguments)
			if args == "{}" {
				args = ""
			} else if len(args) > 80 {
				args = args[:80] + "…"
			}
			toolCalls = append(toolCalls, ToolCallInfo{Name: tc.Function.Name, Args: args})
		}

		// Model requested tools.
		messages = append(messages, m)
		toolMsgs := a.executeToolCalls(ctx, s, m.ToolCalls)
		messages = append(messages, toolMsgs...)
	}

	return "", toolCalls, fmt.Errorf("agent loop: max iterations reached")
}

func truncateAuditErr(err error) string {
	if err == nil {
		return ""
	}
	s := strings.TrimSpace(err.Error())
	if len(s) > 400 {
		s = s[:400] + "…"
	}
	return s
}
