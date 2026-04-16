package openrouter

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestChat_SendsHeadersAndParsesResponse(t *testing.T) {
	var gotAuth string
	var gotTitle string
	var gotPath string
	var gotCT string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotTitle = r.Header.Get("X-Title")
		gotCT = r.Header.Get("Content-Type")
		gotPath = r.URL.Path

		var req ChatRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		_ = json.NewEncoder(w).Encode(ChatResponse{Choices: []struct {
			Message      Message `json:"message"`
			FinishReason string  `json:"finish_reason"`
		}{
			{Message: Message{Role: "assistant", Content: Text("ok")}, FinishReason: "stop"},
		}})
	}))
	defer srv.Close()

	c := New(srv.URL, "k", "App")
	resp, err := c.Chat(context.Background(), ChatRequest{Model: "m", Messages: []Message{{Role: "user", Content: Text("hi")}}})
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/chat/completions" {
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
	if resp.Choices[0].Message.Content == nil || *resp.Choices[0].Message.Content != "ok" {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

func TestChat_Non2xxErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		_, _ = w.Write([]byte("no"))
	}))
	defer srv.Close()

	c := New(srv.URL, "k", "")
	_, err := c.Chat(context.Background(), ChatRequest{Model: "m", Messages: []Message{{Role: "user", Content: Text("hi")}}})
	if err == nil {
		t.Fatalf("expected error")
	}
}

func TestChat_NoChoices(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(ChatResponse{Choices: nil})
	}))
	defer srv.Close()

	c := New(srv.URL, "k", "")
	_, err := c.Chat(context.Background(), ChatRequest{Model: "m", Messages: []Message{{Role: "user", Content: Text("hi")}}})
	if err == nil {
		t.Fatalf("expected error")
	}
}
