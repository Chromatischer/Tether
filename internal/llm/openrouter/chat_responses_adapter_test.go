package openrouter

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestResponsesViaChat_MapsMessagesToolsAndCacheUsage(t *testing.T) {
	var got ChatRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		_ = json.NewEncoder(w).Encode(ChatResponse{
			ID:    "chat_1",
			Model: "deepseek-chat",
			Choices: []struct {
				Message      Message `json:"message"`
				FinishReason string  `json:"finish_reason"`
			}{{
				Message: Message{Role: "assistant", Content: Text("ok"), ToolCalls: []ToolCall{{
					ID: "call_1", Type: "function", Function: ToolCallFunction{Name: "bash", Arguments: `{"cmd":"true"}`},
				}}},
				FinishReason: "tool_calls",
			}},
			Usage: &Usage{PromptTokens: 10, CompletionTokens: 2, TotalTokens: 12, PromptCacheHitTokens: 7, PromptCacheMissTokens: 3},
		})
	}))
	defer srv.Close()

	c := New(srv.URL, "k", "")
	resp, err := c.ResponsesViaChat(context.Background(), ResponsesRequest{
		Model: "deepseek-chat",
		Input: []ResponseItem{{Type: "message", Role: "user", Content: []ContentPart{{Type: "input_text", Text: "hi"}}}},
		Tools: []ResponsesTool{{Type: "function", Name: "bash", Parameters: map[string]any{"type": "object"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Model != "deepseek-chat" || len(got.Messages) != 1 || got.Messages[0].Role != "user" || *got.Messages[0].Content != "hi" {
		t.Fatalf("bad chat request: %+v", got)
	}
	if len(got.Tools) != 1 || got.Tools[0].Function.Name != "bash" {
		t.Fatalf("expected tool mapping, got %+v", got.Tools)
	}
	if resp.Usage == nil || resp.Usage.PromptCacheHitTokens != 7 || resp.Usage.PromptCacheMissTokens != 3 {
		t.Fatalf("expected cache usage mapping, got %+v", resp.Usage)
	}
	if len(resp.Output) != 2 || resp.Output[0].Type != "function_call" || resp.Output[1].Content[0].Text != "ok" {
		t.Fatalf("unexpected responses output: %+v", resp.Output)
	}
}

func TestResponsesStreamViaChat_MapsStreamingToolAndReasoningEvents(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(strings.Join([]string{
			sseDataLine(t, map[string]any{"id": "chat_1", "model": "deepseek-reasoner", "choices": []map[string]any{{"index": 0, "delta": map[string]any{"reasoning_content": "think"}}}}),
			"",
			sseDataLine(t, map[string]any{"id": "chat_1", "model": "deepseek-reasoner", "choices": []map[string]any{{"index": 0, "delta": map[string]any{"content": "ok"}}}}),
			"",
			sseDataLine(t, map[string]any{"id": "chat_1", "model": "deepseek-reasoner", "choices": []map[string]any{{"index": 0, "delta": map[string]any{"tool_calls": []map[string]any{{"index": 0, "id": "call_1", "type": "function", "function": map[string]any{"name": "bash", "arguments": "{\"cmd\":"}}}}}}}),
			"",
			sseDataLine(t, map[string]any{"id": "chat_1", "model": "deepseek-reasoner", "choices": []map[string]any{{"index": 0, "delta": map[string]any{"tool_calls": []map[string]any{{"index": 0, "function": map[string]any{"arguments": "\"true\"}"}}}}}}}),
			"",
			sseDataLine(t, map[string]any{"usage": map[string]any{"prompt_tokens": 10, "completion_tokens": 2, "total_tokens": 12, "prompt_cache_hit_tokens": 6, "prompt_cache_miss_tokens": 4}}),
			"",
			"data: [DONE]",
			"",
		}, "\n")))
	}))
	defer srv.Close()

	var events []ResponsesStreamEvent
	c := New(srv.URL, "k", "")
	resp, err := c.ResponsesStreamViaChat(context.Background(), ResponsesRequest{
		Model: "deepseek-reasoner",
		Input: []ResponseItem{{Type: "message", Role: "user", Content: []ContentPart{{Type: "input_text", Text: "hi"}}}},
	}, func(ev ResponsesStreamEvent) error {
		events = append(events, ev)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var sawText, sawReasoning, sawTool bool
	for _, ev := range events {
		sawText = sawText || ev.Type == "response.output_text.delta" && ev.Delta == "ok"
		sawReasoning = sawReasoning || ev.Type == "response.reasoning_text.delta" && ev.Delta == "think"
		sawTool = sawTool || ev.Type == "response.output_item.added" && ev.Item != nil && ev.Item.Name == "bash"
	}
	if !sawText || !sawReasoning || !sawTool {
		t.Fatalf("missing expected events: text=%v reasoning=%v tool=%v events=%+v", sawText, sawReasoning, sawTool, events)
	}
	if resp.Usage == nil || resp.Usage.PromptCacheHitTokens != 6 || resp.Usage.PromptCacheMissTokens != 4 {
		t.Fatalf("expected cache usage in final response, got %+v", resp.Usage)
	}
	if len(resp.Output) < 3 || resp.Output[1].Type != "function_call" || resp.Output[1].Arguments != `{"cmd":"true"}` {
		t.Fatalf("unexpected final output: %+v", resp.Output)
	}
}
