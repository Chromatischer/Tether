package agent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

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
// Tool events may include truncated result previews for the TUI.
// The final assistant message is still returned via Reply/ReplyStream.
type StreamEvent struct {
	Type string

	Delta string
	Text  string

	Tool ToolCallInfo
	Err  string
}

type toolExecutionPause struct {
	Token string
	Scope string
	Text  string
	Call  openrouter.ResponseItem
}

var confirmScopePattern = regexp.MustCompile(`scope="([^"]+)"`)

func (a *Agent) activeTools(s *toolset.Session, nm *toolNameMap) []openrouter.ResponsesTool {
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
		name := d.Name
		desc := d.Description
		if nm != nil {
			name = nm.ToLLM(name)
			desc = nm.RewriteTextToLLM(desc)
		}
		out = append(out, openrouter.ResponsesTool{
			Type:        "function",
			Name:        name,
			Description: desc,
			Parameters:  d.Parameters,
			Strict:      nil,
		})
	}
	return out
}

func (a *Agent) executeFunctionCalls(ctx context.Context, s *toolset.Session, calls []openrouter.ResponseItem, nm *toolNameMap) (outputs []openrouter.ResponseItem, infos []ToolCallInfo, pause *toolExecutionPause) {
	outputs = make([]openrouter.ResponseItem, 0, len(calls))
	infos = make([]ToolCallInfo, 0, len(calls))

	for _, c := range calls {
		nameLLM := strings.TrimSpace(c.Name)
		name := nameLLM
		if nm != nil {
			name = nm.ToInternal(nameLLM)
		}
		callID := strings.TrimSpace(c.CallID)
		argsStr := strings.TrimSpace(c.Arguments)
		if argsStr == "" {
			argsStr = "{}"
		}
		rawArgsOrig := json.RawMessage(argsStr)
		rawArgsExec := rawArgsOrig

		// Collect tool call info for UI.
		argsUI := strings.TrimSpace(argsStr)
		if argsUI == "{}" {
			argsUI = ""
		} else if len(argsUI) > 80 {
			argsUI = argsUI[:80] + "…"
		}

		// Meta tools accept LLM-visible names in their {name: ...} arguments.
		if nm != nil && (name == "tool.enable" || name == "tool.describe") {
			var obj map[string]any
			if err := json.Unmarshal(rawArgsOrig, &obj); err == nil {
				if v, ok := obj["name"].(string); ok {
					obj["name"] = nm.ToInternal(v)
					if b, err := json.Marshal(obj); err == nil {
						rawArgsExec = json.RawMessage(b)
					}
				}
			}
		}

		impl := a.toolImpl[name]
		var execErr error
		var result any
		if impl == nil {
			execErr = fmt.Errorf("unknown tool: %s. Use tool.search to find the right tool name, then tool.describe before calling it", name)
		} else if !s.IsActive(name) {
			execErr = fmt.Errorf("tool not enabled: %s. Enable it with tool.enable before calling it", name)
		} else {
			if rawArgsExec == nil || len(bytes.TrimSpace(rawArgsExec)) == 0 {
				execErr = fmt.Errorf("invalid tool arguments for %s: missing JSON object; retry with a complete JSON object that matches the tool schema", name)
			} else {
				var parsed any
				if err := json.Unmarshal(rawArgsExec, &parsed); err != nil {
					execErr = fmt.Errorf("invalid tool arguments JSON for %s: %v. Retry with a complete JSON object that matches the tool schema exactly", name, err)
				} else if _, ok := parsed.(map[string]any); !ok {
					execErr = fmt.Errorf("invalid tool arguments for %s: expected a JSON object. Retry with an object matching the tool schema exactly", name)
				}
			}
		}
		if execErr == nil {
			v, err := impl.Execute(ctx, s, rawArgsExec)
			execErr = err
			if err == nil {
				result = v
			}
		}

		if execErr != nil {
			msg := execErr.Error()
			if scope := confirmationScopeFromError(msg); scope != "" && s != nil && s.Confirm != nil {
				reason := confirmationReason(name, argsUI)
				reasonArg := ""
				if name == "confirm.request" {
					var req struct {
						Reason string `json:"reason"`
					}
					_ = json.Unmarshal(rawArgsOrig, &req)
					reasonArg = strings.TrimSpace(req.Reason)
					if reasonArg != "" {
						reason = reasonArg
					}
				}

				token := s.Confirm.Request(s.UserID, scope, reason)
				text := "Confirmation required. Copy this into the chat to continue: `/confirm " + token + "`. Send any other reply to reject it."
				if name == "confirm.request" && reasonArg != "" {
					text = "Confirmation required: " + truncateString(reasonArg, 200) + "\nType `/confirm " + token + "` to approve. Send any other reply to reject it."
				}
				pause = &toolExecutionPause{
					Token: token,
					Scope: scope,
					Text:  text,
					Call:  c,
				}
			}
			if nm != nil {
				msg = nm.RewriteTextToLLM(msg)
			}
			result = map[string]any{"error": msg}
		} else if nm != nil && (name == "tool.search" || name == "tool.describe" || name == "tool.enable") {
			// Rewrite tool names in protocol-shaped outputs.
			jb, _ := json.Marshal(result)
			var anyv any
			if err := json.Unmarshal(jb, &anyv); err == nil {
				result = nm.rewriteAnyStringsToLLM(anyv)
			}
		}

		b, _ := json.Marshal(result)
		infos = append(infos, ToolCallInfo{Name: name, Args: argsUI, Result: formatToolResultPreview(b)})

		// Audit tool call (best-effort). Avoid storing raw args/results.
		if s.DB != nil {
			uid := s.UserID
			sum := sha256.Sum256(rawArgsOrig)
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

		if pause != nil {
			return outputs, infos, pause
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

	return outputs, infos, nil
}

func formatToolResultPreview(raw []byte) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return ""
	}
	var pretty bytes.Buffer
	text := string(raw)
	if err := json.Indent(&pretty, raw, "", "  "); err == nil {
		text = pretty.String()
	}
	if len(text) > 4000 {
		text = text[:4000] + "…"
	}
	return text
}

func (a *Agent) replyWithToolsStream(ctx context.Context, s *toolset.Session, userID, convID int64, baseItems []openrouter.ResponseItem, priorToolCalls []ToolCallInfo, emit func(StreamEvent)) (string, string, []ToolCallInfo, error) {
	items := append([]openrouter.ResponseItem{}, baseItems...)
	toolCalls := append([]ToolCallInfo{}, priorToolCalls...)
	var streamedReasoning strings.Builder
	totalToolCalls := 0
	nextJustifyAt := toolCallJustificationInterval
	justificationPending := false

	nm := newToolNameMap(nil)
	if s != nil {
		nm = newToolNameMap(s.Registry)
	}

	for i := 0; ; i++ {
		tools := a.activeTools(s, nm)
		toolChoice := any("auto")
		maxOutputTokens := 700
		if justificationPending {
			tools = nil
			toolChoice = "none"
			maxOutputTokens = 220
		} else if len(tools) == 0 {
			toolChoice = "none"
		}
		req := openrouter.ResponsesRequest{
			Model:           a.cfg.OpenRouter.Model,
			Input:           items,
			Temperature:     0.2,
			MaxOutputTokens: maxOutputTokens,
			Tools:           tools,
			ToolChoice:      toolChoice,
			Stream:          true,
			Reasoning:       &openrouter.ResponsesReasoning{Effort: "medium"},
			Provider:        a.openRouterProviderPrefs(),
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
						name := c.Name
						if nm != nil {
							name = nm.ToInternal(name)
						}
						emit(StreamEvent{Type: "tool_call", Tool: ToolCallInfo{Name: name, Args: args}})
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
			a.logLLMError("openrouter.responses_stream", req.Model, userID, convID, err)
			if emit != nil {
				emit(StreamEvent{Type: "error", Err: err.Error()})
			}
			return "", "", toolCalls, fmt.Errorf("llm: %w", err)
		}

		// Usage audit (best-effort)
		if s != nil && final.Usage != nil {
			s.TouchActivity()
			s.TotalInputTokens += final.Usage.InputTokens
			s.TotalOutputTokens += final.Usage.OutputTokens
			s.TotalTokens += final.Usage.TotalTokens
			s.TotalCost += final.Usage.Cost
			modelName := strings.TrimSpace(final.Model)
			if modelName == "" {
				modelName = strings.TrimSpace(req.Model)
			}
			s.LastModel = modelName
			s.LastInputTokens = final.Usage.InputTokens
			if info := a.modelInfo(modelName); info.ContextLength > 0 {
				s.LastContextLimit = info.ContextLength
			}
		}
		if s != nil && s.DB != nil && final.Usage != nil {
			payload := map[string]any{
				"model":           req.Model,
				"conversation_id": convID,
				"iteration":       i,
				"input_tokens":    final.Usage.InputTokens,
				"output_tokens":   final.Usage.OutputTokens,
				"total_tokens":    final.Usage.TotalTokens,
				"cost":            final.Usage.Cost,
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
			if justificationPending {
				items = append(items, replayableResponseItems(final.Output)...)
				justificationPending = false
				continue
			}
			if emit != nil {
				emit(StreamEvent{Type: "done", Text: text})
			}
			return text, reasoning, toolCalls, nil
		}

		// Add only replay-safe model output items to history.
		items = append(items, replayableResponseItems(final.Output)...)

		// Execute tools and add function_call_output items.
		toolOutputs, infos, pause := a.executeFunctionCalls(ctx, s, calls, nm)
		toolCalls = append(toolCalls, infos...)
		if emit != nil {
			completedInfos := infos
			if pause != nil && len(completedInfos) > len(toolOutputs) {
				completedInfos = completedInfos[:len(toolOutputs)]
			}
			for _, tc := range completedInfos {
				emit(StreamEvent{Type: "tool_result", Tool: tc})
			}
		}
		if pause != nil {
			items = append(items, toolOutputs...)
			a.storePendingConfirmation(&pendingConfirmation{
				UserID:         userID,
				ConversationID: convID,
				Token:          pause.Token,
				Scope:          pause.Scope,
				Session:        cloneSession(s),
				Items:          append([]openrouter.ResponseItem{}, items...),
				Call:           pause.Call,
				ToolCalls:      append([]ToolCallInfo{}, toolCalls...),
				CreatedAt:      time.Now(),
			})
			if emit != nil {
				emit(StreamEvent{Type: "done", Text: pause.Text})
			}
			return pause.Text, strings.TrimSpace(streamedReasoning.String()), toolCalls, nil
		}
		items = append(items, toolOutputs...)
		totalToolCalls += len(calls)
		if s != nil && len(calls) > 0 {
			s.TouchActivity()
			s.TotalToolCalls += len(calls)
		}
		if totalToolCalls >= nextJustifyAt {
			items = append(items, justificationRequestItem(totalToolCalls))
			nextJustifyAt = nextJustificationThreshold(totalToolCalls)
			justificationPending = true
		}
	}
}

func confirmationScopeFromError(msg string) string {
	match := confirmScopePattern.FindStringSubmatch(strings.TrimSpace(msg))
	if len(match) != 2 {
		return ""
	}
	return strings.TrimSpace(match[1])
}

func confirmationReason(name string, args string) string {
	name = strings.TrimSpace(name)
	args = strings.TrimSpace(args)
	if name == "" {
		return "Approve this tool action."
	}
	if args == "" {
		return "Approve tool use: " + name + "."
	}
	return "Approve tool use: " + name + " " + args
}

const toolCallJustificationInterval = 25

func nextJustificationThreshold(totalToolCalls int) int {
	if totalToolCalls < 0 {
		totalToolCalls = 0
	}
	return ((totalToolCalls / toolCallJustificationInterval) + 1) * toolCallJustificationInterval
}

func justificationRequestItem(totalToolCalls int) openrouter.ResponseItem {
	text := fmt.Sprintf(
		"You have used %d tools in this run. Before doing more tool work, justify it briefly. "+
			"Explain what you have learned so far, what remains unresolved, why additional tool use is still necessary, and the concrete stopping condition. "+
			"If you are looping or not making meaningful progress, stop now and return to the user instead of continuing. "+
			"Do not call any tools in this response.",
		totalToolCalls,
	)
	return openrouter.ResponseItem{
		Type:    "message",
		Role:    "system",
		Content: []openrouter.ContentPart{{Type: "input_text", Text: text}},
	}
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
