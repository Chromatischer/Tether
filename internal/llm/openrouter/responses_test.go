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
