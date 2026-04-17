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

// StreamEvent is an optional callback payload used by the TUI to render incremental output.
// It is best-effort and may omit some details.
//
// Types:
// - assistant_delta: incremental assistant text
// - tool_call: model requested a tool
// - tool_result: tool finished (success/failure)
// - done: final assistant message
// - error: fatal error
//
// Note: For safety, tool results are not streamed by default; only tool names and (truncated) args.
// The final assistant message is still returned via Reply/ReplyStream.
type StreamEvent struct {
	Type string

	Delta string
	Text  string

	Tool ToolCallInfo
	Err  string
}

func (a *Agent) activeTools(s *toolset.Session) []openrouter.ResponsesTool {
	defs := make([]toolset.ToolDef, 0, len(s.Active))
	for name := range s.Active {
		impl := a.toolImpl[name]
		if impl == nil {
			continue
		}
		defs = append(defs, impl.Definition())
	}
	sort.Slice(defs, func(i, j int) bool { return defs[i].Name < defs[j].Name })
	out := make([]openrouter.ResponsesTool, 0, len(defs))
	for _, d := range defs {
		out = append(out, openrouter.ResponsesTool{
			Type:        "function",
			Name:        d.Name,
			Description: d.Description,
			Parameters:  d.Parameters,
			Strict:      nil,
		})
	}
	return out
}

func (a *Agent) executeFunctionCalls(ctx context.Context, s *toolset.Session, calls []openrouter.ResponseItem) (outputs []openrouter.ResponseItem, infos []ToolCallInfo) {
	outputs = make([]openrouter.ResponseItem, 0, len(calls))
	infos = make([]ToolCallInfo, 0, len(calls))

	for _, c := range calls {
		name := strings.TrimSpace(c.Name)
		callID := strings.TrimSpace(c.CallID)
		argsStr := strings.TrimSpace(c.Arguments)
		if argsStr == "" {
			argsStr = "{}"
		}
		rawArgs := json.RawMessage(argsStr)

		// Collect tool call info for UI.
		argsUI := strings.TrimSpace(argsStr)
		if argsUI == "{}" {
			argsUI = ""
		} else if len(argsUI) > 80 {
			argsUI = argsUI[:80] + "…"
		}
		infos = append(infos, ToolCallInfo{Name: name, Args: argsUI})

		impl := a.toolImpl[name]
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
				"call_id":        callID,
				"args_sha256":    hex.EncodeToString(sum[:]),
				"ok":             execErr == nil,
				"error":          truncateAuditErr(execErr),
				"active_tools_n": len(s.Active),
			}
			pb, _ := json.Marshal(payload)
			_ = store.AddAuditEvent(s.DB, &uid, "tool_call", string(pb))
		}

		outID := "fco_" + callID
		if strings.TrimSpace(callID) == "" {
			// Should not happen, but keep the loop alive.
			outID = "fco_" + name
		}
		outputs = append(outputs, openrouter.ResponseItem{
			Type:   "function_call_output",
			ID:     outID,
			CallID: callID,
			Output: string(b),
		})
	}

	return outputs, infos
}

func (a *Agent) replyWithToolsStream(ctx context.Context, s *toolset.Session, userID, convID int64, baseItems []openrouter.ResponseItem, emit func(StreamEvent)) (string, string, []ToolCallInfo, error) {
	items := append([]openrouter.ResponseItem{}, baseItems...)
	const maxIterations = 8
	toolCalls := make([]ToolCallInfo, 0, 8)
	var streamedReasoning strings.Builder

	for i := 0; i < maxIterations; i++ {
		req := openrouter.ResponsesRequest{
			Model:           a.cfg.OpenRouter.Model,
			Input:           items,
			Temperature:     0.2,
			MaxOutputTokens: 700,
			Tools:           a.activeTools(s),
			ToolChoice:      "auto",
			Stream:          true,
			Reasoning:       &openrouter.ResponsesReasoning{Effort: "medium"},
		}

		// Accumulate tool calls as they arrive.
		pendingCallsByOutputIdx := map[int]*openrouter.ResponseItem{}
		pendingCalls := make([]*openrouter.ResponseItem, 0, 4)

		// Best-effort incremental text stream.
		var streamed strings.Builder

		final, err := a.llm.ResponsesStream(ctx, req, func(ev openrouter.ResponsesStreamEvent) error {
			switch ev.Type {
			case "response.output_item.added":
				if ev.Item != nil && ev.Item.Type == "function_call" {
					// Copy so we can mutate args later.
					c := *ev.Item
					pendingCalls = append(pendingCalls, &c)
					if ev.OutputIndex != nil {
						pendingCallsByOutputIdx[*ev.OutputIndex] = &c
					}
					if emit != nil {
						args := strings.TrimSpace(c.Arguments)
						if args == "{}" {
							args = ""
						} else if len(args) > 80 {
							args = args[:80] + "…"
						}
						emit(StreamEvent{Type: "tool_call", Tool: ToolCallInfo{Name: c.Name, Args: args}})
					}
				} else if ev.Item != nil && ev.Item.Type == "reasoning" {
					emitReasoningSummaryDelta(&streamedReasoning, reasoningSummaryText(ev.Item.Summary), emit)
				}
			case "response.output_item.done":
				if ev.Item != nil && ev.Item.Type == "reasoning" {
					emitReasoningSummaryDelta(&streamedReasoning, reasoningSummaryText(ev.Item.Summary), emit)
				}
			case "response.function_call_arguments.done":
				if strings.TrimSpace(ev.Arguments) == "" {
					return nil
				}
				// Attach args to the best candidate call.
				if ev.OutputIndex != nil {
					if c := pendingCallsByOutputIdx[*ev.OutputIndex]; c != nil {
						c.Arguments = ev.Arguments
						return nil
					}
				}
				for j := len(pendingCalls) - 1; j >= 0; j-- {
					if strings.TrimSpace(pendingCalls[j].Arguments) == "" {
						pendingCalls[j].Arguments = ev.Arguments
						break
					}
				}
			case "response.content_part.delta":
				if ev.Delta != "" {
					streamed.WriteString(ev.Delta)
					if emit != nil {
						emit(StreamEvent{Type: "assistant_delta", Delta: ev.Delta, Text: streamed.String()})
					}
				}
			case "response.output_text.delta":
				if ev.Delta != "" {
					streamed.WriteString(ev.Delta)
					if emit != nil {
						emit(StreamEvent{Type: "assistant_delta", Delta: ev.Delta, Text: streamed.String()})
					}
				}
			case "response.reasoning.delta", "response.reasoning_text.delta":
				if ev.Delta != "" {
					streamedReasoning.WriteString(ev.Delta)
				}
				if ev.Delta != "" && emit != nil {
					emit(StreamEvent{Type: "reasoning_delta", Delta: ev.Delta})
				}
			}
			return nil
		})
		if err != nil {
			if emit != nil {
				emit(StreamEvent{Type: "error", Err: err.Error()})
			}
			return "", "", toolCalls, fmt.Errorf("llm: %w", err)
		}

		// Usage audit (best-effort)
		if s != nil && s.DB != nil && final.Usage != nil {
			payload := map[string]any{
				"model":           req.Model,
				"conversation_id": convID,
				"iteration":       i,
				"input_tokens":    final.Usage.InputTokens,
				"output_tokens":   final.Usage.OutputTokens,
				"total_tokens":    final.Usage.TotalTokens,
				"tools_n":         len(req.Tools),
			}
			pb, _ := json.Marshal(payload)
			uid := userID
			_ = store.AddAuditEvent(s.DB, &uid, "llm_usage", string(pb))
		}

		// Identify tool calls in the completed response.
		calls := make([]openrouter.ResponseItem, 0, 4)
		for _, it := range final.Output {
			if it.Type == "function_call" {
				calls = append(calls, it)
			}
		}

		if len(calls) == 0 {
			text := strings.TrimSpace(extractResponsesText(final))
			if text == "" {
				text = strings.TrimSpace(streamed.String())
			}
			reasoning := strings.TrimSpace(streamedReasoning.String())
			if reasoning == "" {
				reasoning = extractResponsesReasoning(final)
			}
			if emit != nil {
				emit(StreamEvent{Type: "done", Text: text})
			}
			return text, reasoning, toolCalls, nil
		}

		// Append tool call info for display.
		for _, c := range calls {
			args := strings.TrimSpace(c.Arguments)
			if args == "{}" {
				args = ""
			} else if len(args) > 80 {
				args = args[:80] + "…"
			}
			toolCalls = append(toolCalls, ToolCallInfo{Name: c.Name, Args: args})
		}

		// Add only replay-safe model output items to history.
		items = append(items, replayableResponseItems(final.Output)...)

		// Execute tools and add function_call_output items.
		toolOutputs, infos := a.executeFunctionCalls(ctx, s, calls)
		if emit != nil {
			for _, tc := range infos {
				emit(StreamEvent{Type: "tool_result", Tool: tc})
			}
		}
		items = append(items, toolOutputs...)
	}

	return "", strings.TrimSpace(streamedReasoning.String()), toolCalls, fmt.Errorf("agent loop: max iterations reached")
}

func emitReasoningSummaryDelta(dst *strings.Builder, summary string, emit func(StreamEvent)) {
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return
	}
	current := dst.String()
	if current == summary {
		return
	}
	if strings.HasPrefix(summary, current) {
		delta := summary[len(current):]
		dst.WriteString(delta)
		if delta != "" && emit != nil {
			emit(StreamEvent{Type: "reasoning_delta", Delta: delta})
		}
		return
	}
	if current != "" {
		dst.Reset()
	}
	dst.WriteString(summary)
	if emit != nil {
		emit(StreamEvent{Type: "reasoning_delta", Delta: summary})
	}
}

func reasoningSummaryText(parts []openrouter.ReasoningSummaryPart) string {
	lines := make([]string, 0, len(parts))
	for _, part := range parts {
		if text := strings.TrimSpace(part.Text); text != "" {
			lines = append(lines, text)
		}
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func replayableResponseItems(items []openrouter.ResponseItem) []openrouter.ResponseItem {
	if len(items) == 0 {
		return nil
	}
	out := make([]openrouter.ResponseItem, 0, len(items))
	for _, it := range items {
		switch it.Type {
		case "message", "function_call":
			out = append(out, it)
		}
	}
	return out
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
