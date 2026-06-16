package toolset

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestIsBlockedFetchIP(t *testing.T) {
	cases := []struct {
		ip      string
		blocked bool
	}{
		{"127.0.0.1", true},
		{"::1", true},
		{"169.254.169.254", true}, // cloud metadata
		{"10.0.0.5", true},
		{"172.16.0.1", true},
		{"192.168.1.1", true},
		{"0.0.0.0", true},
		{"fc00::1", true}, // unique-local
		{"fe80::1", true}, // link-local
		{"8.8.8.8", false},
		{"1.1.1.1", false},
		{"93.184.216.34", false}, // example.com
	}
	for _, c := range cases {
		ip := net.ParseIP(c.ip)
		if got := isBlockedFetchIP(ip); got != c.blocked {
			t.Errorf("isBlockedFetchIP(%s) = %v, want %v", c.ip, got, c.blocked)
		}
	}
}

func TestSafeFetchHTTPClient_BlocksLoopback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// Default (allowPrivate=false) must refuse the loopback test server.
	client := safeFetchHTTPClient(5*time.Second, false)
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
	if _, err := client.Do(req); err == nil {
		t.Fatal("expected loopback fetch to be blocked, got nil error")
	} else {
		var blocked *blockedAddrError
		if !errors.As(err, &blocked) {
			t.Fatalf("expected blockedAddrError, got %T: %v", err, err)
		}
	}

	// With allowPrivate=true the same fetch must succeed.
	client = safeFetchHTTPClient(5*time.Second, true)
	req, _ = http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("expected loopback fetch to succeed with allowPrivate, got %v", err)
	}
	resp.Body.Close()
}
