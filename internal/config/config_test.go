package config

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func writeTempConfig(t *testing.T, dir string, yaml string) string {
	t.Helper()
	p := filepath.Join(dir, "tether.yaml")
	if err := os.WriteFile(p, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoad_DefaultsAndRequiredFields(t *testing.T) {
	dir := t.TempDir()
	h, err := bcrypt.GenerateFromPassword([]byte("pw"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	p := writeTempConfig(t, dir, "ssh:\n  portal_password_hash: \""+string(h)+"\"\n")

	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.LogLevel == "" {
		t.Fatalf("expected log_level default to be set")
	}
	if cfg.DB.Path == "" {
		t.Fatalf("expected db.path default")
	}
	if cfg.SSH.ListenAddr != ":2222" {
		t.Fatalf("expected ssh.listen_addr default :2222, got %q", cfg.SSH.ListenAddr)
	}
	if cfg.SSH.HostKeyPath == "" {
		t.Fatalf("expected ssh.host_key_path default")
	}
	if cfg.SSH.AuthorizedKeysPath == "" {
		t.Fatalf("expected ssh.authorized_keys_path default")
	}
	if cfg.OpenRouter.BaseURL == "" || cfg.OpenRouter.Model == "" {
		t.Fatalf("expected openrouter defaults")
	}
	if cfg.Secrets.TTLHours != 24 {
		t.Fatalf("expected secrets.ttl_hours default 24, got %d", cfg.Secrets.TTLHours)
	}
	if cfg.Signal.SignalCLIPath != "signal-cli" {
		t.Fatalf("expected signal.signal_cli_path default signal-cli, got %q", cfg.Signal.SignalCLIPath)
	}
	if cfg.Signal.HTTPAddr != "127.0.0.1:17800" {
		t.Fatalf("expected signal.http_addr default 127.0.0.1:17800, got %q", cfg.Signal.HTTPAddr)
	}
}

func TestLoad_RequiresPortalPasswordHash(t *testing.T) {
	dir := t.TempDir()
	p := writeTempConfig(t, dir, "log_level: info\n")
	_, err := Load(p)
	if err == nil {
		t.Fatalf("expected error")
	}
}

func TestLoad_EnvFallbacks(t *testing.T) {
	dir := t.TempDir()
	h, _ := bcrypt.GenerateFromPassword([]byte("pw"), bcrypt.MinCost)

	t.Setenv("OPENROUTER_API_KEY", "k-openrouter")
	t.Setenv("TETHER_MASTER_KEY", "k-master")
	t.Setenv("TETHER_SIGNAL_NUMBER", "+123")

	p := writeTempConfig(t, dir, "ssh:\n  portal_password_hash: \""+string(h)+"\"\nopenrouter: {}\nsecrets: {}\nsignal: {}\n")
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.OpenRouter.APIKey != "k-openrouter" {
		t.Fatalf("expected OPENROUTER_API_KEY fallback")
	}
	if cfg.Secrets.MasterKey != "k-master" {
		t.Fatalf("expected TETHER_MASTER_KEY fallback")
	}
	if cfg.Signal.AccountNumber != "+123" {
		t.Fatalf("expected TETHER_SIGNAL_NUMBER fallback")
	}
}
