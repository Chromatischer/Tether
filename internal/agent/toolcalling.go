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
	"tether/internal/config"
	"tether/internal/llm/openrouter"
	"tether/internal/store"
)

// reasoningConfig maps the configured reasoning effort to the OpenRouter
// Responses reasoning object. The special value "auto" turns on adaptive
// reasoning: reasoning is enabled, but no fixed effort is pinned so the model
// decides how much to spend. Any other value is passed through as the effort.
func reasoningConfig(effort string) *openrouter.ResponsesReasoning {
	if strings.EqualFold(strings.TrimSpace(effort), config.ReasoningEffortAuto) {
		enabled := true
		return &openrouter.ResponsesReasoning{Enabled: &enabled}
	}
	return &openrouter.ResponsesReasoning{Effort: effort}
}

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
	Token  string
	Scope  string
	Text   string
	Reason string
	Call   openrouter.ResponseItem
}

var confirmScopePattern = regexp.MustCompile(`scope="([^"]+)"`)

func (a *Agent) activeTools(s *toolset.Session, nm *toolNameMap) []openrouter.ResponsesTool {
	defs := make([]toolset.ToolDef, 0, len(s.Active))
	for name := range s.Active {
		// view_image is only offered to vision-capable models.
		if name == "view_image" && (s == nil || !s.VisionEnabled) {
			continue
		}
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
		// (tool.enable takes a category, not a tool name, so it is excluded.)
		if nm != nil && name == "tool.describe" {
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
			hint := "Enable its category with tool.enable before calling it"
			if a.registry != nil {
				if spec, ok := a.registry.Get(name); ok && spec.Category != "" {
					hint = fmt.Sprintf("Enable it with tool.enable {category: %q}", spec.Category)
				}
			}
			execErr = fmt.Errorf("tool not enabled: %s. %s", name, hint)
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
				if name == "admin.bash" {
					var req struct {
						Command       string `json:"command"`
						Justification string `json:"justification"`
					}
					_ = json.Unmarshal(rawArgsOrig, &req)
					if j := strings.TrimSpace(req.Justification); j != "" {
						reason = "HOST (unsandboxed) command: " + truncateString(strings.TrimSpace(req.Command), 200) + " — justification: " + truncateString(j, 200)
					}
				}

				token := s.Confirm.Request(s.UserID, scope, reason)
				text := "Paused — waiting for user to approve this action. Do not retry this command and do not call any other tools. The user will approve or decline."
				if name == "confirm.request" && reasonArg != "" {
					text = "Paused — waiting for user confirmation: " + truncateString(reasonArg, 200) + ". Do not call any tools until the user responds."
				}
				pause = &toolExecutionPause{
					Token:  token,
					Scope:  scope,
					Text:   text,
					Call:   c,
					Reason: reason,
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

func mergeFinalFunctionCallsWithPending(final []openrouter.ResponseItem, pending []*openrouter.ResponseItem) []openrouter.ResponseItem {
	if len(final) == 0 || len(pending) == 0 {
		return final
	}

	byCallID := make(map[string]openrouter.ResponseItem, len(pending))
	ordered := make([]openrouter.ResponseItem, 0, len(pending))
	for _, item := range pending {
		if item == nil || item.Type != "function_call" {
			continue
		}
		ordered = append(ordered, *item)
		if callID := strings.TrimSpace(item.CallID); callID != "" {
			byCallID[callID] = *item
		}
	}
	if len(ordered) == 0 {
		return final
	}

	nextOrdered := 0
	for i := range final {
		if final[i].Type != "function_call" {
			continue
		}
		if callID := strings.TrimSpace(final[i].CallID); callID != "" {
			if pendingItem, ok := byCallID[callID]; ok {
				if len(strings.TrimSpace(pendingItem.Arguments)) > len(strings.TrimSpace(final[i].Arguments)) {
					final[i].Arguments = pendingItem.Arguments
				}
				if final[i].Name == "" {
					final[i].Name = pendingItem.Name
				}
				continue
			}
		}
		for nextOrdered < len(ordered) {
			pendingItem := ordered[nextOrdered]
			nextOrdered++
			if len(strings.TrimSpace(pendingItem.Arguments)) > len(strings.TrimSpace(final[i].Arguments)) {
				final[i].Arguments = pendingItem.Arguments
			}
			if final[i].Name == "" {
				final[i].Name = pendingItem.Name
			}
			break
		}
	}
	return final
}

func (a *Agent) replyWithToolsStream(ctx context.Context, s *toolset.Session, userID, convID int64, baseItems []openrouter.ResponseItem, priorToolCalls []ToolCallInfo, emit func(StreamEvent)) (string, string, []ToolCallInfo, []store.ReasoningBlock, error) {
	items := append([]openrouter.ResponseItem{}, baseItems...)
	toolCalls := append([]ToolCallInfo{}, priorToolCalls...)
	var streamedReasoning strings.Builder
	totalToolCalls := 0
	nextJustifyAt := toolCallJustificationInterval
	justificationPending := false
	// strippedReasoning guards a one-shot retry: if a replayed (cross-turn or
	// cross-provider) reasoning block fails signature validation, we drop all
	// reasoning items and try again rather than failing the whole turn.
	strippedReasoning := false

	nm := newToolNameMap(nil)
	if s != nil {
		nm = newToolNameMap(s.Registry)
	}

	// Gate the view_image tool on the active model's vision capability.
	if s != nil {
		s.VisionEnabled = a.modelSupportsVision(a.cfg.OpenRouter.Model)
		if s.Active == nil {
			s.Active = map[string]bool{}
		}
		if s.VisionEnabled && s.IsAllowed("view_image") {
			s.Active["view_image"] = true
		} else {
			delete(s.Active, "view_image")
		}
	}

	for i := 0; ; i++ {
		tools := a.activeTools(s, nm)
		toolChoice := any("auto")
		if justificationPending {
			tools = nil
			toolChoice = "none"
		} else if len(tools) == 0 {
			toolChoice = "none"
		}
		req := openrouter.ResponsesRequest{
			Model:       a.cfg.LLMModel(),
			Input:       items,
			Temperature: a.cfg.AgentTemperature(),
			Tools:       tools,
			ToolChoice:  toolChoice,
			Stream:      true,
			Reasoning:   reasoningConfig(a.cfg.AgentReasoningEffort()),
			// Ask for the signed/encrypted reasoning so it can be replayed across
			// tool-call iterations without tripping provider signature validation.
			Include:  []string{"reasoning.encrypted_content"},
			Provider: a.openRouterProviderPrefs(),
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
				args := ev.Arguments
				if args == "" {
					args = ev.Delta
				}
				if strings.TrimSpace(args) == "" {
					return nil
				}
				// Attach args to the best candidate call.
				if ev.OutputIndex != nil {
					if c := pendingCallsByOutputIdx[*ev.OutputIndex]; c != nil {
						c.Arguments = args
						return nil
					}
				}
				for j := len(pendingCalls) - 1; j >= 0; j-- {
					if strings.TrimSpace(pendingCalls[j].Arguments) == "" {
						pendingCalls[j].Arguments = args
						break
					}
				}
			case "response.function_call_arguments.delta":
				args := ev.Delta
				if args == "" {
					args = ev.Arguments
				}
				if strings.TrimSpace(args) == "" {
					return nil
				}
				if ev.OutputIndex != nil {
					if c := pendingCallsByOutputIdx[*ev.OutputIndex]; c != nil {
						c.Arguments += args
						return nil
					}
				}
				for j := len(pendingCalls) - 1; j >= 0; j-- {
					if pendingCalls[j] != nil {
						pendingCalls[j].Arguments += args
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
			// A replayed reasoning block (from a prior turn, or generated by a
			// provider the request later fell back off of) can fail Anthropic's
			// signature check. Drop reasoning items once and retry instead of
			// failing the turn.
			if !strippedReasoning && isThinkingSignatureError(err) {
				strippedReasoning = true
				items = stripReasoningItems(items)
				i--
				continue
			}
			a.logLLMError(a.cfg.LLMProvider()+".responses_stream", req.Model, userID, convID, err)
			if emit != nil {
				emit(StreamEvent{Type: "error", Err: err.Error()})
			}
			return "", "", toolCalls, nil, fmt.Errorf("llm: %w", err)
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
				"provider":        a.cfg.LLMProvider(),
				"conversation_id": convID,
				"iteration":       i,
				"input_tokens":    final.Usage.InputTokens,
				"output_tokens":   final.Usage.OutputTokens,
				"total_tokens":    final.Usage.TotalTokens,
				"cost":            final.Usage.Cost,
				"tools_n":         len(req.Tools),
			}
			if final.Usage.PromptCacheHitTokens > 0 || final.Usage.PromptCacheMissTokens > 0 {
				payload["prompt_cache_hit_tokens"] = final.Usage.PromptCacheHitTokens
				payload["prompt_cache_miss_tokens"] = final.Usage.PromptCacheMissTokens
			}
			if final.Usage.ReasoningTokens > 0 {
				payload["reasoning_tokens"] = final.Usage.ReasoningTokens
			}
			pb, _ := json.Marshal(payload)
			uid := userID
			_ = store.AddAuditEvent(s.DB, &uid, "llm_usage", string(pb))
		}

		final.Output = mergeFinalFunctionCallsWithPending(final.Output, pendingCalls)

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
			// Capture the signed reasoning behind the final answer so callers can
			// persist it and replay it on later turns.
			return text, reasoning, toolCalls, reasoningBlocksFromItems(final.Output), nil
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
			if img := imageMessageItem(s.DrainPendingImages()); img != nil {
				items = append(items, *img)
			}
			a.storePendingConfirmation(&pendingConfirmation{
				UserID:         userID,
				ConversationID: convID,
				Token:          pause.Token,
				Scope:          pause.Scope,
				Reason:         pause.Reason,
				Session:        cloneSession(s),
				Items:          append([]openrouter.ResponseItem{}, items...),
				Call:           pause.Call,
				ToolCalls:      append([]ToolCallInfo{}, toolCalls...),
				CreatedAt:      time.Now(),
			})
			if emit != nil {
				emit(StreamEvent{Type: "done", Text: pause.Text})
			}
			return pause.Text, strings.TrimSpace(streamedReasoning.String()), toolCalls, nil, nil
		}
		items = append(items, toolOutputs...)
		if img := imageMessageItem(s.DrainPendingImages()); img != nil {
			items = append(items, *img)
		}
		totalToolCalls += len(calls)
		if s != nil && len(calls) > 0 {
			s.TouchActivity()
			s.TotalToolCalls += len(calls)
		}
		if maxToolCalls := a.cfg.AgentMaxToolCalls(); totalToolCalls >= maxToolCalls {
			text := fmt.Sprintf(
				"Stopped after reaching the per-turn tool-call limit (%d). "+
					"I did not finish on my own. Tell me to continue, or narrow the task so it needs fewer steps.",
				maxToolCalls,
			)
			if emit != nil {
				emit(StreamEvent{Type: "done", Text: text})
			}
			return text, strings.TrimSpace(streamedReasoning.String()), toolCalls, nil, nil
		}
		if totalToolCalls >= nextJustifyAt {
			items = append(items, justificationRequestItem(totalToolCalls))
			nextJustifyAt = nextJustificationThreshold(totalToolCalls)
			justificationPending = true
		}
	}
}

// imageMessageItem builds a user message carrying images queued by the
// view_image tool so a vision-capable model can actually see them. Returns nil
// when there are no images.
func imageMessageItem(imgs []toolset.PendingImage) *openrouter.ResponseItem {
	if len(imgs) == 0 {
		return nil
	}
	parts := make([]openrouter.ContentPart, 0, len(imgs)+1)
	names := make([]string, 0, len(imgs))
	for _, im := range imgs {
		if strings.TrimSpace(im.DataURL) == "" {
			continue
		}
		names = append(names, im.Path)
		parts = append(parts, openrouter.ContentPart{Type: "input_image", ImageURL: im.DataURL})
	}
	if len(parts) == 0 {
		return nil
	}
	label := "Image content loaded via view_image: " + strings.Join(names, ", ")
	content := append([]openrouter.ContentPart{{Type: "input_text", Text: label}}, parts...)
	return &openrouter.ResponseItem{Type: "message", Role: "user", Content: content}
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
		case "reasoning":
			// Only replay reasoning that carries the provider's signed/encrypted
			// payload. Summary-only reasoning (e.g. Anthropic via the Responses
			// API, which we don't request encrypted_content for) cannot be
			// replayed: OpenRouter rebuilds it into a thinking block with no valid
			// signature, and Anthropic/Bedrock/Vertex reject the follow-up turn
			// with "Invalid signature in thinking block". Dropping it is safe —
			// the assistant's text and tool calls are preserved below.
			if strings.TrimSpace(it.EncryptedContent) == "" {
				continue
			}
			out = append(out, it)
		case "message", "function_call":
			out = append(out, it)
		}
	}
	return out
}

// stripReasoningItems returns items with all reasoning entries removed. Used to
// recover from a thinking-block signature rejection by retrying without any
// replayed reasoning.
func stripReasoningItems(items []openrouter.ResponseItem) []openrouter.ResponseItem {
	out := make([]openrouter.ResponseItem, 0, len(items))
	for _, it := range items {
		if it.Type == "reasoning" {
			continue
		}
		out = append(out, it)
	}
	return out
}

// reasoningBlocksFromItems extracts replay-safe (signed) reasoning blocks from a
// completed response so they can be persisted and replayed on later turns.
func reasoningBlocksFromItems(items []openrouter.ResponseItem) []store.ReasoningBlock {
	var blocks []store.ReasoningBlock
	for _, it := range items {
		if it.Type != "reasoning" {
			continue
		}
		if strings.TrimSpace(it.EncryptedContent) == "" {
			continue
		}
		blocks = append(blocks, store.ReasoningBlock{
			ItemID:           strings.TrimSpace(it.ID),
			EncryptedContent: it.EncryptedContent,
		})
	}
	return blocks
}

// isThinkingSignatureError reports whether err is Anthropic's rejection of a
// thinking block whose signature it cannot verify.
func isThinkingSignatureError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "signature") && strings.Contains(msg, "thinking")
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
