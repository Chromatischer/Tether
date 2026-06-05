package openrouter

import (
	"context"
	"fmt"
	"strings"
)

func (c *Client) ResponsesViaChat(ctx context.Context, req ResponsesRequest) (ResponsesResponse, error) {
	chatReq, err := chatRequestFromResponses(req)
	if err != nil {
		return ResponsesResponse{}, err
	}
	resp, err := c.Chat(ctx, chatReq)
	if err != nil {
		return ResponsesResponse{}, err
	}
	return responsesFromChat(resp), nil
}

func (c *Client) ResponsesStreamViaChat(ctx context.Context, req ResponsesRequest, onEvent func(ev ResponsesStreamEvent) error) (ResponsesResponse, error) {
	chatReq, err := chatRequestFromResponses(req)
	if err != nil {
		return ResponsesResponse{}, err
	}
	chatReq.Stream = true

	emittedTools := map[int]bool{}
	final, err := c.ChatStream(ctx, chatReq, func(chunk ChatStreamChunk) error {
		for _, choice := range chunk.Choices {
			if choice.Delta.Content != "" && onEvent != nil {
				if err := onEvent(ResponsesStreamEvent{Type: "response.output_text.delta", Delta: choice.Delta.Content}); err != nil {
					return err
				}
			}
			if choice.Delta.ReasoningContent != "" && onEvent != nil {
				if err := onEvent(ResponsesStreamEvent{Type: "response.reasoning_text.delta", Delta: choice.Delta.ReasoningContent}); err != nil {
					return err
				}
			}
			for _, tc := range choice.Delta.ToolCalls {
				idx := choice.Index
				if tc.Index != nil {
					idx = *tc.Index
				}
				if !emittedTools[idx] && (tc.ID != "" || tc.Function.Name != "") {
					emittedTools[idx] = true
					if onEvent != nil {
						item := ResponseItem{Type: "function_call", ID: tc.ID, CallID: tc.ID, Name: tc.Function.Name, Arguments: tc.Function.Arguments}
						if err := onEvent(ResponsesStreamEvent{Type: "response.output_item.added", OutputIndex: &idx, Item: &item}); err != nil {
							return err
						}
					}
				}
				if tc.Function.Arguments != "" && onEvent != nil {
					if err := onEvent(ResponsesStreamEvent{Type: "response.function_call_arguments.delta", OutputIndex: &idx, Delta: tc.Function.Arguments}); err != nil {
						return err
					}
				}
			}
		}
		return nil
	})
	if err != nil {
		return ResponsesResponse{}, err
	}
	out := responsesFromChat(final)
	if onEvent != nil {
		if err := onEvent(ResponsesStreamEvent{Type: "response.done", Response: &out}); err != nil {
			return out, err
		}
	}
	return out, nil
}

func chatRequestFromResponses(req ResponsesRequest) (ChatRequest, error) {
	messages, err := messagesFromResponsesInput(req.Input)
	if err != nil {
		return ChatRequest{}, err
	}
	tools := make([]Tool, 0, len(req.Tools))
	for _, t := range req.Tools {
		if strings.TrimSpace(t.Name) == "" {
			continue
		}
		tools = append(tools, Tool{
			Type: "function",
			Function: ToolFunction{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.Parameters,
				Strict:      t.Strict,
			},
		})
	}
	chatReq := ChatRequest{
		Model:       req.Model,
		Messages:    messages,
		Temperature: req.Temperature,
		MaxTokens:   req.MaxOutputTokens,
		TopP:        req.TopP,
		Tools:       tools,
		ToolChoice:  req.ToolChoice,
	}
	if req.ToolChoice == nil && len(tools) > 0 {
		chatReq.ToolChoice = "auto"
	}
	if req.Reasoning != nil && strings.TrimSpace(req.Reasoning.Effort) != "" {
		chatReq.ReasoningEffort = strings.TrimSpace(req.Reasoning.Effort)
	}
	return chatReq, nil
}

func messagesFromResponsesInput(input any) ([]Message, error) {
	switch v := input.(type) {
	case string:
		return []Message{{Role: "user", Content: Text(v)}}, nil
	case []ResponseItem:
		return messagesFromResponseItems(v), nil
	case []any:
		items := make([]ResponseItem, 0, len(v))
		for _, raw := range v {
			item, ok := raw.(ResponseItem)
			if !ok {
				return nil, fmt.Errorf("chat adapter: unsupported input item %T", raw)
			}
			items = append(items, item)
		}
		return messagesFromResponseItems(items), nil
	default:
		return nil, fmt.Errorf("chat adapter: unsupported input type %T", input)
	}
}

func messagesFromResponseItems(items []ResponseItem) []Message {
	messages := make([]Message, 0, len(items))
	for _, item := range items {
		switch item.Type {
		case "message":
			text := contentPartsText(item.Content)
			messages = append(messages, Message{Role: item.Role, Content: Text(text)})
		case "function_call":
			callID := strings.TrimSpace(item.CallID)
			if callID == "" {
				callID = item.ID
			}
			messages = append(messages, Message{
				Role:    "assistant",
				Content: Text(""),
				ToolCalls: []ToolCall{{
					ID:   callID,
					Type: "function",
					Function: ToolCallFunction{
						Name:      item.Name,
						Arguments: item.Arguments,
					},
				}},
			})
		case "function_call_output":
			messages = append(messages, Message{Role: "tool", Content: Text(item.Output), ToolCallID: item.CallID})
		}
	}
	return messages
}

func contentPartsText(parts []ContentPart) string {
	var b strings.Builder
	for _, p := range parts {
		b.WriteString(p.Text)
	}
	return b.String()
}

func responsesFromChat(resp ChatResponse) ResponsesResponse {
	out := ResponsesResponse{
		ID:        resp.ID,
		Object:    "response",
		CreatedAt: resp.Created,
		Model:     resp.Model,
		Status:    "completed",
	}
	if resp.Usage != nil {
		out.Usage = &ResponsesUsage{
			InputTokens:           resp.Usage.PromptTokens,
			OutputTokens:          resp.Usage.CompletionTokens,
			TotalTokens:           resp.Usage.TotalTokens,
			Cost:                  resp.Usage.Cost,
			PromptCacheHitTokens:  resp.Usage.PromptCacheHitTokens,
			PromptCacheMissTokens: resp.Usage.PromptCacheMissTokens,
		}
		if resp.Usage.PromptTokensDetails != nil {
			out.Usage.PromptCacheHitTokens = resp.Usage.PromptTokensDetails.CachedTokens
		}
		if resp.Usage.CompletionTokensDetails != nil {
			out.Usage.ReasoningTokens = resp.Usage.CompletionTokensDetails.ReasoningTokens
		}
	}
	if len(resp.Choices) == 0 {
		return out
	}
	msg := resp.Choices[0].Message
	if text := strings.TrimSpace(msg.ReasoningContent); text != "" {
		out.Output = append(out.Output, ResponseItem{
			Type:    "reasoning",
			Summary: []ReasoningSummaryPart{{Text: text}},
		})
	}
	for _, tc := range msg.ToolCalls {
		callID := strings.TrimSpace(tc.ID)
		out.Output = append(out.Output, ResponseItem{
			Type:      "function_call",
			ID:        callID,
			CallID:    callID,
			Name:      tc.Function.Name,
			Arguments: tc.Function.Arguments,
		})
	}
	if msg.Content != nil && *msg.Content != "" {
		out.Output = append(out.Output, ResponseItem{
			Type:    "message",
			Role:    "assistant",
			Content: []ContentPart{{Type: "output_text", Text: *msg.Content}},
		})
	}
	return out
}
