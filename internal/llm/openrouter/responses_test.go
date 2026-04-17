package openrouter

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

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

func TestReasoningSummaryPart_UnmarshalObjectFallback(t *testing.T) {
	var part ReasoningSummaryPart
	if err := json.Unmarshal([]byte(`{"text":"hello"}`), &part); err != nil {
		t.Fatal(err)
	}
	if part.Text != "hello" {
		t.Fatalf("expected hello, got %q", part.Text)
	}
}
