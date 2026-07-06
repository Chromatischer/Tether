package config

import (
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type AdminEnv struct {
	OpenRouterAPIKey         string `yaml:"openrouter_api_key,omitempty"`
	OpenRouterModel          string `yaml:"openrouter_model,omitempty"`
	OpenRouterSecondaryModel string `yaml:"openrouter_secondary_model,omitempty"`
	DiscordBotToken          string `yaml:"discord_bot_token,omitempty"`
	DiscordEnabled           *bool  `yaml:"discord_enabled,omitempty"`
	SignalNumber             string `yaml:"signal_number,omitempty"`
	MasterKey                string `yaml:"master_key,omitempty"`
}

func AdminEnvPath(dataDir string) string {
	return filepath.Join(dataDir, "admin", "env.yaml")
}

func LoadAdminEnv(dataDir string) (AdminEnv, error) {
	var env AdminEnv
	path := AdminEnvPath(dataDir)
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return env, nil
		}
		return env, err
	}
	if err := yaml.Unmarshal(b, &env); err != nil {
		return env, err
	}
	env.normalize()
	return env, nil
}

func SaveAdminEnv(dataDir string, env AdminEnv) error {
	env.normalize()
	path := AdminEnvPath(dataDir)
	if env.empty() {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := yaml.Marshal(env)
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}

func (e *AdminEnv) normalize() {
	e.OpenRouterAPIKey = strings.TrimSpace(e.OpenRouterAPIKey)
	e.OpenRouterModel = strings.TrimSpace(e.OpenRouterModel)
	e.OpenRouterSecondaryModel = strings.TrimSpace(e.OpenRouterSecondaryModel)
	e.DiscordBotToken = strings.TrimSpace(e.DiscordBotToken)
	e.SignalNumber = strings.TrimSpace(e.SignalNumber)
	e.MasterKey = strings.TrimSpace(e.MasterKey)
}

func (e AdminEnv) empty() bool {
	return e.OpenRouterAPIKey == "" &&
		e.OpenRouterModel == "" &&
		e.OpenRouterSecondaryModel == "" &&
		e.DiscordBotToken == "" &&
		e.DiscordEnabled == nil &&
		e.SignalNumber == "" &&
		e.MasterKey == ""
}
