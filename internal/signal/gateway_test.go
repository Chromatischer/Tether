package signal

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"tether/internal/config"
	"tether/internal/testutil"
)

func newTestGateway(t *testing.T, serverURL string) *Gateway {
	t.Helper()
	cfg := &config.Config{}
	cfg.Signal.HTTPAddr = serverURL
	g := NewGateway(cfg, testutil.OpenTestDB(t), nil)
	return g
}

func TestBaseURL(t *testing.T) {
	cfg := &config.Config{}
	cfg.Signal.HTTPAddr = "127.0.0.1:17800"
	if got := NewGateway(cfg, testutil.OpenTestDB(t), nil).baseURL(); got != "http://127.0.0.1:17800" {
		t.Fatalf("unexpected base URL: %q", got)
	}

	cfg.Signal.HTTPAddr = "https://signal.example"
	if got := NewGateway(cfg, testutil.OpenTestDB(t), nil).baseURL(); got != "https://signal.example" {
		t.Fatalf("expected existing scheme to be preserved, got %q", got)
	}
}

func TestWaitForCheckUsesInjectedHTTPClient(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/api/v1/check" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if calls < 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	g := newTestGateway(t, srv.URL)
	g.http = srv.Client()
	if err := g.waitForCheck(context.Background(), 2*time.Second); err != nil {
		t.Fatalf("expected successful check, got %v", err)
	}
	if calls != 2 {
		t.Fatalf("expected retry before success, got %d calls", calls)
	}
}

func TestConsumeEventsReturnsStatusError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad gateway", http.StatusBadGateway)
	}))
	defer srv.Close()

	g := newTestGateway(t, srv.URL)
	g.http = srv.Client()
	err := g.consumeEvents(context.Background())
	if err == nil || !strings.Contains(err.Error(), "events status 502") {
		t.Fatalf("expected status error, got %v", err)
	}
}

func TestSendPostsJSONRPCAndReturnsTimestamp(t *testing.T) {
	var gotMethod string
	var gotRecipient []string
	var gotMessage string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/rpc" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Fatalf("unexpected content type: %q", ct)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		var req struct {
			Method string `json:"method"`
			Params struct {
				Recipient []string `json:"recipient"`
				Message   string   `json:"message"`
			} `json:"params"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatal(err)
		}
		gotMethod = req.Method
		gotRecipient = req.Params.Recipient
		gotMessage = req.Params.Message
		_, _ = w.Write([]byte(`{"result":{"timestamp":12345}}`))
	}))
	defer srv.Close()

	g := newTestGateway(t, srv.URL)
	g.http = srv.Client()
	ts, err := g.send(context.Background(), "+49123", "hello")
	if err != nil {
		t.Fatalf("expected send success, got %v", err)
	}
	if ts != 12345 {
		t.Fatalf("unexpected timestamp: %d", ts)
	}
	if gotMethod != "send" || len(gotRecipient) != 1 || gotRecipient[0] != "+49123" || gotMessage != "hello" {
		t.Fatalf("unexpected rpc payload: method=%q recipient=%v message=%q", gotMethod, gotRecipient, gotMessage)
	}
}

func TestSendReturnsStatusError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusBadRequest)
	}))
	defer srv.Close()

	g := newTestGateway(t, srv.URL)
	g.http = srv.Client()
	_, err := g.send(context.Background(), "+49123", "hello")
	if err == nil || !strings.Contains(err.Error(), "rpc status 400") {
		t.Fatalf("expected rpc status error, got %v", err)
	}
}
