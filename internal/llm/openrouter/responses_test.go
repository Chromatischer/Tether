package openrouter

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func sseDataLine(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return "data: " + string(b)
}

func TestResponses_SendsHeadersAndParsesResponse(t *testing.T) {
	var gotAuth string
	var gotTitle string
	var gotPath string
	var gotCT string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotTitle = r.Header.Get("X-Title")
		gotCT = r.Header.Get("Content-Type")
		gotPath = r.URL.Path

		var req ResponsesRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		_ = json.NewEncoder(w).Encode(ResponsesResponse{
			ID:     "resp_123",
			Status: "completed",
			Output: []ResponseItem{{
				Type: "message",
				Role: "assistant",
				Content: []ContentPart{{
					Type: "output_text",
					Text: "ok",
				}},
			}},
		})
	}))
	defer srv.Close()

	c := New(srv.URL, "k", "App")
	resp, err := c.Responses(context.Background(), ResponsesRequest{
		Model: "m",
		Input: []ResponseItem{{Type: "message", Role: "user", Content: []ContentPart{{Type: "input_text", Text: "hi"}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/responses" {
		t.Fatalf("unexpected path %q", gotPath)
	}
	if gotAuth != "Bearer k" {
		t.Fatalf("unexpected auth %q", gotAuth)
	}
	if gotTitle != "App" {
		t.Fatalf("unexpected X-Title %q", gotTitle)
	}
	if gotCT != "application/json" {
		t.Fatalf("unexpected content-type %q", gotCT)
	}
	if len(resp.Output) != 1 || resp.Output[0].Content[0].Text != "ok" {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

func TestResponses_Non2xxErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		_, _ = w.Write([]byte("no"))
	}))
	defer srv.Close()

	c := New(srv.URL, "k", "")
	_, err := c.Responses(context.Background(), ResponsesRequest{
		Model: "m",
		Input: []ResponseItem{{Type: "message", Role: "user", Content: []ContentPart{{Type: "input_text", Text: "hi"}}}},
	})
	if err == nil {
		t.Fatalf("expected error")
	}
}

func TestResponsesStream_ParsesDoneResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(strings.Join([]string{
			"data: {\"type\":\"response.output_item.added\",\"output_index\":0,\"item\":{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"o\"}]}}",
			"",
			"data: {\"type\":\"response.content_part.delta\",\"delta\":\"o\"}",
			"",
			"data: {\"type\":\"response.done\",\"response\":{\"id\":\"resp_1\",\"status\":\"completed\",\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"ok\"}]}]}}",
			"",
			"data: [DONE]",
			"",
		}, "\n")))
	}))
	defer srv.Close()

	seenDelta := false
	c := New(srv.URL, "k", "")
	resp, err := c.ResponsesStream(context.Background(), ResponsesRequest{
		Model: "m",
		Input: []ResponseItem{{Type: "message", Role: "user", Content: []ContentPart{{Type: "input_text", Text: "hi"}}}},
	}, func(ev ResponsesStreamEvent) error {
		if ev.Type == "response.content_part.delta" && ev.Delta == "o" {
			seenDelta = true
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !seenDelta {
		t.Fatalf("expected to observe content delta")
	}
	if resp.ID != "resp_1" || len(resp.Output) != 1 || resp.Output[0].Content[0].Text != "ok" {
		t.Fatalf("unexpected final response: %+v", resp)
	}
}

func TestResponsesStream_ReturnsTopLevelErrorMessage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(strings.Join([]string{
			"data: {\"type\":\"error\",\"error\":{\"message\":\"upstream overloaded\"}}",
			"",
			"data: [DONE]",
			"",
		}, "\n")))
	}))
	defer srv.Close()

	c := New(srv.URL, "k", "")
	_, err := c.ResponsesStream(context.Background(), ResponsesRequest{
		Model: "m",
		Input: []ResponseItem{{Type: "message", Role: "user", Content: []ContentPart{{Type: "input_text", Text: "hi"}}}},
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "upstream overloaded") {
		t.Fatalf("expected stream error message, got %v", err)
	}
}

func TestResponsesStream_ReturnsReasonWhenErrorMessageMissing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(strings.Join([]string{
			"data: {\"type\":\"error\",\"reason\":\"error\"}",
			"",
			"data: [DONE]",
			"",
		}, "\n")))
	}))
	defer srv.Close()

	c := New(srv.URL, "k", "")
	_, err := c.ResponsesStream(context.Background(), ResponsesRequest{
		Model: "m",
		Input: []ResponseItem{{Type: "message", Role: "user", Content: []ContentPart{{Type: "input_text", Text: "hi"}}}},
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "stream closed with reason: error") {
		t.Fatalf("expected reason fallback, got %v", err)
	}
}

func TestResponsesStream_ParsesReasoningDelta(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(strings.Join([]string{
			"data: {\"type\":\"response.reasoning.delta\",\"delta\":\"step 1\"}",
			"",
			"data: {\"type\":\"response.done\",\"response\":{\"id\":\"resp_2\",\"status\":\"completed\",\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"ok\"}]}]}}",
			"",
			"data: [DONE]",
			"",
		}, "\n")))
	}))
	defer srv.Close()

	var reasoning string
	c := New(srv.URL, "k", "")
	_, err := c.ResponsesStream(context.Background(), ResponsesRequest{
		Model: "m",
		Input: []ResponseItem{{Type: "message", Role: "user", Content: []ContentPart{{Type: "input_text", Text: "hi"}}}},
	}, func(ev ResponsesStreamEvent) error {
		if ev.Type == "response.reasoning.delta" {
			reasoning += ev.Delta
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if reasoning != "step 1" {
		t.Fatalf("expected reasoning delta, got %q", reasoning)
	}
}

func TestResponsesStream_ParsesReasoningTextDelta(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(strings.Join([]string{
			"data: {\"type\":\"response.reasoning_text.delta\",\"delta\":\"step 1\"}",
			"",
			"data: {\"type\":\"response.done\",\"response\":{\"id\":\"resp_2b\",\"status\":\"completed\",\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"ok\"}]}]}}",
			"",
			"data: [DONE]",
			"",
		}, "\n")))
	}))
	defer srv.Close()

	var reasoning string
	c := New(srv.URL, "k", "")
	_, err := c.ResponsesStream(context.Background(), ResponsesRequest{
		Model: "m",
		Input: []ResponseItem{{Type: "message", Role: "user", Content: []ContentPart{{Type: "input_text", Text: "hi"}}}},
	}, func(ev ResponsesStreamEvent) error {
		if ev.Type == "response.reasoning_text.delta" {
			reasoning += ev.Delta
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if reasoning != "step 1" {
		t.Fatalf("expected reasoning text delta, got %q", reasoning)
	}
}

func TestResponsesStream_ReconstructsOutputFromOutputItemDone(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(strings.Join([]string{
			"data: {\"type\":\"response.output_item.added\",\"output_index\":0,\"item\":{\"type\":\"message\",\"id\":\"msg_1\",\"role\":\"assistant\",\"status\":\"in_progress\",\"content\":[]}}",
			"",
			"data: {\"type\":\"response.output_item.done\",\"output_index\":0,\"item\":{\"type\":\"message\",\"id\":\"msg_1\",\"role\":\"assistant\",\"status\":\"completed\",\"content\":[{\"type\":\"output_text\",\"text\":\"hello\"}]}}",
			"",
			"data: {\"type\":\"response.done\",\"response\":{\"id\":\"resp_3\",\"status\":\"completed\"}}",
			"",
			"data: [DONE]",
			"",
		}, "\n")))
	}))
	defer srv.Close()

	c := New(srv.URL, "k", "")
	resp, err := c.ResponsesStream(context.Background(), ResponsesRequest{
		Model: "m",
		Input: []ResponseItem{{Type: "message", Role: "user", Content: []ContentPart{{Type: "input_text", Text: "hi"}}}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Output) != 1 || resp.Output[0].Content[0].Text != "hello" {
		t.Fatalf("expected reconstructed output, got %+v", resp)
	}
}

func TestResponsesStream_PreservesReasoningSummaryItem(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(strings.Join([]string{
			"data: {\"type\":\"response.output_item.done\",\"output_index\":0,\"item\":{\"type\":\"reasoning\",\"id\":\"rs_1\",\"summary\":[\"first\",\"second\"]}}",
			"",
			"data: {\"type\":\"response.done\",\"response\":{\"id\":\"resp_4\",\"status\":\"completed\"}}",
			"",
			"data: [DONE]",
			"",
		}, "\n")))
	}))
	defer srv.Close()

	c := New(srv.URL, "k", "")
	resp, err := c.ResponsesStream(context.Background(), ResponsesRequest{
		Model: "m",
		Input: []ResponseItem{{Type: "message", Role: "user", Content: []ContentPart{{Type: "input_text", Text: "hi"}}}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Output) != 1 || resp.Output[0].Type != "reasoning" {
		t.Fatalf("expected reconstructed reasoning item, got %+v", resp)
	}
	if len(resp.Output[0].Summary) != 2 || resp.Output[0].Summary[0].Text != "first" || resp.Output[0].Summary[1].Text != "second" {
		t.Fatalf("expected reasoning summary to survive reconstruction, got %+v", resp.Output[0])
	}
}

func TestResponsesStream_PreservesReasoningEncryptedContent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(strings.Join([]string{
			"data: {\"type\":\"response.output_item.added\",\"output_index\":0,\"item\":{\"type\":\"reasoning\",\"id\":\"rs_1\"}}",
			"",
			"data: {\"type\":\"response.output_item.done\",\"output_index\":0,\"item\":{\"type\":\"reasoning\",\"id\":\"rs_1\",\"encrypted_content\":\"signed-blob\"}}",
			"",
			"data: {\"type\":\"response.done\",\"response\":{\"id\":\"resp_enc\",\"status\":\"completed\"}}",
			"",
			"data: [DONE]",
			"",
		}, "\n")))
	}))
	defer srv.Close()

	c := New(srv.URL, "k", "")
	resp, err := c.ResponsesStream(context.Background(), ResponsesRequest{
		Model: "m",
		Input: []ResponseItem{{Type: "message", Role: "user", Content: []ContentPart{{Type: "input_text", Text: "hi"}}}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Output) != 1 || resp.Output[0].Type != "reasoning" {
		t.Fatalf("expected reconstructed reasoning item, got %+v", resp)
	}
	if resp.Output[0].EncryptedContent != "signed-blob" {
		t.Fatalf("expected encrypted_content to survive reconstruction, got %+v", resp.Output[0])
	}
}

func TestResponsesStream_ReconstructsFunctionCallArgumentsFromDeltas(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(strings.Join([]string{
			sseDataLine(t, map[string]any{
				"type":         "response.output_item.added",
				"output_index": 0,
				"item": map[string]any{
					"type":      "function_call",
					"id":        "fc_1",
					"call_id":   "call_1",
					"name":      "bash",
					"arguments": "",
				},
			}),
			"",
			sseDataLine(t, map[string]any{
				"type":         "response.function_call_arguments.delta",
				"output_index": 0,
				"delta":        `{"command":"echo \"`,
			}),
			"",
			sseDataLine(t, map[string]any{
				"type":         "response.function_call_arguments.delta",
				"output_index": 0,
				"delta":        `hi\""}`,
			}),
			"",
			sseDataLine(t, map[string]any{
				"type": "response.done",
				"response": map[string]any{
					"id":     "resp_fc_1",
					"status": "completed",
				},
			}),
			"",
			"data: [DONE]",
			"",
		}, "\n")))
	}))
	defer srv.Close()

	c := New(srv.URL, "k", "")
	resp, err := c.ResponsesStream(context.Background(), ResponsesRequest{
		Model: "m",
		Input: []ResponseItem{{Type: "message", Role: "user", Content: []ContentPart{{Type: "input_text", Text: "hi"}}}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Output) != 1 || resp.Output[0].Type != "function_call" {
		t.Fatalf("expected reconstructed function_call output, got %+v", resp)
	}
	if resp.Output[0].Arguments != "{\"command\":\"echo \\\"hi\\\"\"}" {
		t.Fatalf("expected reconstructed arguments, got %q", resp.Output[0].Arguments)
	}
}

func TestResponsesStream_MergesFunctionCallArgumentsIntoResponseDoneOutput(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(strings.Join([]string{
			sseDataLine(t, map[string]any{
				"type":         "response.output_item.added",
				"output_index": 0,
				"item": map[string]any{
					"type":      "function_call",
					"id":        "fc_2",
					"call_id":   "call_2",
					"name":      "write",
					"arguments": "",
				},
			}),
			"",
			sseDataLine(t, map[string]any{
				"type":         "response.function_call_arguments.delta",
				"output_index": 0,
				"delta":        `{"path":"todo.txt",`,
			}),
			"",
			sseDataLine(t, map[string]any{
				"type":         "response.function_call_arguments.delta",
				"output_index": 0,
				"delta":        `"content":"hello"}`,
			}),
			"",
			sseDataLine(t, map[string]any{
				"type": "response.done",
				"response": map[string]any{
					"id":     "resp_fc_2",
					"status": "completed",
					"output": []map[string]any{{
						"type":      "function_call",
						"id":        "fc_2",
						"call_id":   "call_2",
						"name":      "write",
						"arguments": "",
					}},
				},
			}),
			"",
			"data: [DONE]",
			"",
		}, "\n")))
	}))
	defer srv.Close()

	c := New(srv.URL, "k", "")
	resp, err := c.ResponsesStream(context.Background(), ResponsesRequest{
		Model: "m",
		Input: []ResponseItem{{Type: "message", Role: "user", Content: []ContentPart{{Type: "input_text", Text: "hi"}}}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Output) != 1 || resp.Output[0].Type != "function_call" {
		t.Fatalf("expected function_call output, got %+v", resp)
	}
	if resp.Output[0].Arguments != "{\"path\":\"todo.txt\",\"content\":\"hello\"}" {
		t.Fatalf("expected merged arguments, got %q", resp.Output[0].Arguments)
	}
}

func TestReasoningSummaryPart_UnmarshalObjectFallback(t *testing.T) {
	var part ReasoningSummaryPart
	if err := json.Unmarshal([]byte(`{"text":"hello"}`), &part); err != nil {
		t.Fatal(err)
	}
	if part.Text != "hello" {
		t.Fatalf("expected hello, got %q", part.Text)
	}
}

// Regression: a reasoning item echoed back into the input array must serialize
// its summary parts as {"type":"summary_text","text":...}. Previously the
// zero-tag struct marshaled as {"Text":...}, which OpenRouter rejected with
// 400 invalid_prompt (missing "type", wrong-cased "text").
func TestReasoningSummaryPart_MarshalsResponsesAPIShape(t *testing.T) {
	item := ResponseItem{
		Type:    "reasoning",
		Summary: []ReasoningSummaryPart{{Text: "because reasons"}},
	}
	b, err := json.Marshal(item)
	if err != nil {
		t.Fatal(err)
	}

	var got struct {
		Type    string `json:"type"`
		Summary []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"summary"`
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal marshaled item: %v (raw: %s)", err, b)
	}
	if got.Type != "reasoning" {
		t.Fatalf("expected reasoning item, got %q (raw: %s)", got.Type, b)
	}
	if len(got.Summary) != 1 {
		t.Fatalf("expected 1 summary part, got %d (raw: %s)", len(got.Summary), b)
	}
	if got.Summary[0].Type != "summary_text" {
		t.Fatalf("expected summary_text, got %q (raw: %s)", got.Summary[0].Type, b)
	}
	if got.Summary[0].Text != "because reasons" {
		t.Fatalf("expected text preserved, got %q (raw: %s)", got.Summary[0].Text, b)
	}

	// Round-trips back through the reader (which accepts the object form).
	var back ReasoningSummaryPart
	partRaw, _ := json.Marshal(item.Summary[0])
	if err := json.Unmarshal(partRaw, &back); err != nil {
		t.Fatalf("round-trip unmarshal: %v", err)
	}
	if back.Text != "because reasons" {
		t.Fatalf("round-trip lost text: %q", back.Text)
	}
}

func TestResponsesRequest_IncludesEncryptedReasoning(t *testing.T) {
	b, err := json.Marshal(ResponsesRequest{
		Model:   "anthropic/claude-x",
		Input:   "hi",
		Include: []string{"reasoning.encrypted_content"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"include":["reasoning.encrypted_content"]`) {
		t.Fatalf("expected include field in request, got %s", b)
	}
}
