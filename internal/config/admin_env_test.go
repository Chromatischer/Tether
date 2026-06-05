package config

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestSaveLoadAdminEnv(t *testing.T) {
	dir := t.TempDir()
	in := AdminEnv{
		OpenRouterAPIKey: "openrouter-key",
		OpenRouterModel:  "openai/gpt-5.4-mini",
		DiscordBotToken:  "discord-token",
		SignalNumber:     "+4912345",
		MasterKey:        "master-key",
	}
	if err := SaveAdminEnv(dir, in); err != nil {
		t.Fatalf("SaveAdminEnv: %v", err)
	}

	got, err := LoadAdminEnv(dir)
	if err != nil {
		t.Fatalf("LoadAdminEnv: %v", err)
	}
	if got != in {
		t.Fatalf("unexpected admin env: %#v", got)
	}
}

func TestSaveAdminEnvRemovesEmptyFile(t *testing.T) {
	dir := t.TempDir()
	if err := SaveAdminEnv(dir, AdminEnv{OpenRouterAPIKey: "x"}); err != nil {
		t.Fatalf("seed SaveAdminEnv: %v", err)
	}
	if err := SaveAdminEnv(dir, AdminEnv{}); err != nil {
		t.Fatalf("clear SaveAdminEnv: %v", err)
	}
	if _, err := LoadAdminEnv(dir); err != nil {
		t.Fatalf("LoadAdminEnv after clear: %v", err)
	}
	if _, err := os.Stat(AdminEnvPath(dir)); !os.IsNotExist(err) {
		t.Fatalf("expected env file removed, got err=%v", err)
	}
}

func TestLoad_AdminEnvFallbacks(t *testing.T) {
	dir := t.TempDir()
	h, _ := bcrypt.GenerateFromPassword([]byte("pw"), bcrypt.MinCost)
	if err := SaveAdminEnv(dir, AdminEnv{
		OpenRouterAPIKey: "admin-openrouter",
		OpenRouterModel:  "openai/gpt-5.4-mini",
		DiscordBotToken:  "admin-discord",
		SignalNumber:     "+498765",
		MasterKey:        "admin-master",
	}); err != nil {
		t.Fatalf("SaveAdminEnv: %v", err)
	}

	p := writeTempConfig(t, dir, "ssh:\n  portal_password_hash: \""+string(h)+"\"\npaths:\n  data_dir: "+filepath.ToSlash(dir)+"\nopenrouter: {}\nsecrets: {}\nsignal: {}\ndiscord: {}\n")
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.OpenRouter.APIKey != "admin-openrouter" {
		t.Fatalf("expected admin openrouter fallback, got %q", cfg.OpenRouter.APIKey)
	}
	if cfg.OpenRouter.Model != "openai/gpt-5.4-mini" {
		t.Fatalf("expected admin openrouter model override, got %q", cfg.OpenRouter.Model)
	}
	if cfg.Discord.BotToken != "admin-discord" {
		t.Fatalf("expected admin discord fallback, got %q", cfg.Discord.BotToken)
	}
	if cfg.Signal.AccountNumber != "+498765" {
		t.Fatalf("expected admin signal fallback, got %q", cfg.Signal.AccountNumber)
	}
	if cfg.Secrets.MasterKey != "admin-master" {
		t.Fatalf("expected admin master key fallback, got %q", cfg.Secrets.MasterKey)
	}
}

func TestLoad_AdminEnvDiscordEnabledOverride(t *testing.T) {
	dir := t.TempDir()
	h, _ := bcrypt.GenerateFromPassword([]byte("pw"), bcrypt.MinCost)
	on := true
	if err := SaveAdminEnv(dir, AdminEnv{DiscordEnabled: &on}); err != nil {
		t.Fatalf("SaveAdminEnv: %v", err)
	}

	// yaml leaves discord.enabled unset (false); admin env must flip it on.
	p := writeTempConfig(t, dir, "ssh:\n  portal_password_hash: \""+string(h)+"\"\npaths:\n  data_dir: "+filepath.ToSlash(dir)+"\nopenrouter: {}\nsecrets: {}\nsignal: {}\ndiscord: {}\n")
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.Discord.Enabled {
		t.Fatalf("expected admin env to enable discord gateway")
	}

	// And it must be authoritative when turning it back off.
	off := false
	if err := SaveAdminEnv(dir, AdminEnv{DiscordEnabled: &off}); err != nil {
		t.Fatalf("SaveAdminEnv off: %v", err)
	}
	p2 := writeTempConfig(t, dir, "ssh:\n  portal_password_hash: \""+string(h)+"\"\npaths:\n  data_dir: "+filepath.ToSlash(dir)+"\nopenrouter: {}\nsecrets: {}\nsignal: {}\ndiscord:\n  enabled: true\n")
	cfg2, err := Load(p2)
	if err != nil {
		t.Fatalf("Load off: %v", err)
	}
	if cfg2.Discord.Enabled {
		t.Fatalf("expected admin env to disable discord gateway over yaml enabled=true")
	}
}

func TestLoad_ConfigOverridesAdminEnv(t *testing.T) {
	dir := t.TempDir()
	h, _ := bcrypt.GenerateFromPassword([]byte("pw"), bcrypt.MinCost)
	if err := SaveAdminEnv(dir, AdminEnv{
		OpenRouterAPIKey: "admin-openrouter",
		OpenRouterModel:  "openai/gpt-5.4-mini",
		DiscordBotToken:  "admin-discord",
		SignalNumber:     "+498765",
		MasterKey:        "admin-master",
	}); err != nil {
		t.Fatalf("SaveAdminEnv: %v", err)
	}

	yaml := "ssh:\n  portal_password_hash: \"" + string(h) + "\"\n" +
		"paths:\n  data_dir: " + filepath.ToSlash(dir) + "\n" +
		"openrouter:\n  api_key: cfg-openrouter\n" +
		"secrets:\n  master_key: cfg-master\n" +
		"signal:\n  account_number: \"+49111\"\n" +
		"discord:\n  bot_token: cfg-discord\n"
	p := writeTempConfig(t, dir, yaml)
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.OpenRouter.APIKey != "cfg-openrouter" ||
		cfg.Secrets.MasterKey != "cfg-master" ||
		cfg.Signal.AccountNumber != "+49111" ||
		cfg.Discord.BotToken != "cfg-discord" {
		t.Fatalf("config values should override admin env: %#v", cfg)
	}
	if cfg.OpenRouter.Model != "openai/gpt-5.4-mini" {
		t.Fatalf("expected admin env model override, got %q", cfg.OpenRouter.Model)
	}
}
