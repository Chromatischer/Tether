package mcp

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"tether/internal/config"
)

func TestNewManagerInfosAndDefaults(t *testing.T) {
	enabled := true
	cfg := &config.Config{}
	cfg.MCP.Enabled = &enabled
	cfg.MCP.Servers = []config.MCPServer{
		{Name: " zeta ", Command: "zeta", DefaultEnabledForAllUsers: true},
		{Name: "alpha", Command: "alpha", Transport: "stdio"},
	}

	m := NewManager(cfg)
	infos := m.Infos()
	if len(infos) != 2 || infos[0].Name != "alpha" || infos[1].Name != "zeta" {
		t.Fatalf("expected sorted infos, got %+v", infos)
	}
	if infos[1].Transport != "stdio" {
		t.Fatalf("expected stdio default transport, got %+v", infos[1])
	}

	got := m.DefaultEnabledServers()
	if !got["zeta"] || got["alpha"] {
		t.Fatalf("unexpected default-enabled servers: %+v", got)
	}
}

func TestToolsForServerReturnsCopy(t *testing.T) {
	enabled := true
	cfg := &config.Config{}
	cfg.MCP.Enabled = &enabled
	cfg.MCP.Servers = []config.MCPServer{{Name: "alpha", Command: "alpha"}}

	m := NewManager(cfg)
	m.mu.Lock()
	m.servers["alpha"].tools = append(m.servers["alpha"].tools, nil)
	m.mu.Unlock()

	tools, ok := m.ToolsForServer("alpha")
	if !ok || len(tools) != 1 {
		t.Fatalf("unexpected tools response: %+v ok=%v", tools, ok)
	}
	tools[0] = nil
	tools2, _ := m.ToolsForServer("alpha")
	if len(tools2) != 1 {
		t.Fatalf("expected original tools slice to remain intact, got %+v", tools2)
	}
}

func TestConfirmScopeStableIgnoresConfirmToken(t *testing.T) {
	a := ConfirmScope(" server ", " tool ", map[string]any{"x": 1, "confirm_token": "abc"})
	b := ConfirmScope("server", "tool", map[string]any{"confirm_token": "def", "x": 1})
	if a != b {
		t.Fatalf("expected stable scope ignoring confirm token, got %q vs %q", a, b)
	}
	if !strings.HasPrefix(a, "mcp:server:tool:") {
		t.Fatalf("unexpected scope format: %q", a)
	}
}

func TestMergedEnvAndExpandEnvValue(t *testing.T) {
	t.Setenv("TETHER_MCP_TEST", "expanded")
	got := mergedEnv([]string{"B=base", "A=old"}, map[string]string{
		" A ": "new",
		"C":   "${env:TETHER_MCP_TEST}",
		" ":   "ignored",
	})
	if strings.Join(got, ",") != "A=new,B=base,C=expanded" {
		t.Fatalf("unexpected merged env: %+v", got)
	}
	if got := expandEnvValue("${env: }"); got != "" {
		t.Fatalf("expected empty env expansion for blank key, got %q", got)
	}
	if got := expandEnvValue(" literal "); got != "literal" {
		t.Fatalf("expected trimmed literal value, got %q", got)
	}
}

func TestConnectValidationErrors(t *testing.T) {
	m := NewManager(&config.Config{})
	if _, err := m.connect(context.Background(), config.MCPServer{Name: "bad", Transport: "http", Command: "cmd"}); err == nil {
		t.Fatal("expected unsupported transport error")
	}
	if _, err := m.connect(context.Background(), config.MCPServer{Name: "bad"}); err == nil {
		t.Fatal("expected missing command error")
	}
}

func TestPruneIdleLockedRemovesNilSessions(t *testing.T) {
	enabled := true
	cfg := &config.Config{}
	cfg.MCP.Enabled = &enabled
	cfg.MCP.Servers = []config.MCPServer{{Name: "alpha", Command: "alpha"}}
	m := NewManager(cfg)
	m.mu.Lock()
	m.servers["alpha"].sessions[1] = nil
	m.servers["alpha"].sessions[2] = &userSession{lastUsed: time.Now().Add(-time.Hour)}
	m.mu.Unlock()

	m.pruneIdleLocked(time.Now())

	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.servers["alpha"].sessions) != 0 {
		t.Fatalf("expected idle and nil sessions removed, got %+v", m.servers["alpha"].sessions)
	}
}

func TestReloadConfigDisablesServersWhenMCPDisabled(t *testing.T) {
	enabled := false
	cfg := &config.Config{}
	cfg.MCP.Enabled = &enabled
	cfg.MCP.Servers = []config.MCPServer{{Name: "alpha", Command: "alpha"}}
	m := NewManager(cfg)
	if len(m.Infos()) != 0 {
		t.Fatalf("expected no MCP servers when disabled, got %+v", m.Infos())
	}
}

func TestMustStableJSONFallback(t *testing.T) {
	b := mustStableJSON(map[string]any{"x": 1})
	if string(b) != `{"x":1}` {
		t.Fatalf("unexpected stable json: %s", string(b))
	}

	orig := os.Stdout
	_ = orig
	if got := mustStableJSON(map[string]any{"bad": func() {}}); string(got) != "{}" {
		t.Fatalf("expected fallback json object, got %s", string(got))
	}
}

func TestGetOrConnectSessionUnknownServer(t *testing.T) {
	m := NewManager(&config.Config{})
	_, err := m.getOrConnectSession(context.Background(), 1, "missing")
	if err == nil || !errors.Is(err, err) {
		t.Fatalf("expected unknown server error, got %v", err)
	}
}
