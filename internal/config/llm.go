package config

import "strings"

func (c *Config) LLMProvider() string {
	if c == nil {
		return "openrouter"
	}
	p := strings.ToLower(strings.TrimSpace(c.LLM.Provider))
	if p == "" {
		return "openrouter"
	}
	return p
}

func (c *Config) LLMAPIKey() string {
	if c == nil {
		return ""
	}
	switch c.LLMProvider() {
	case "deepseek":
		return c.DeepSeek.APIKey
	default:
		return c.OpenRouter.APIKey
	}
}

func (c *Config) LLMBaseURL() string {
	if c == nil {
		return ""
	}
	switch c.LLMProvider() {
	case "deepseek":
		return c.DeepSeek.BaseURL
	default:
		return c.OpenRouter.BaseURL
	}
}

func (c *Config) LLMModel() string {
	if c == nil {
		return ""
	}
	switch c.LLMProvider() {
	case "deepseek":
		return c.DeepSeek.Model
	default:
		return c.OpenRouter.Model
	}
}

func (c *Config) LLMAPIKeyEnvName() string {
	switch c.LLMProvider() {
	case "deepseek":
		return "DEEPSEEK_API_KEY"
	default:
		return "OPENROUTER_API_KEY"
	}
}
