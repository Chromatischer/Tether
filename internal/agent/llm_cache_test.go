package agent

import (
	"testing"

	"tether/internal/config"
	"tether/internal/llm/openrouter"
)

func TestResponsesCacheKeySeparatesProviders(t *testing.T) {
	req := openrouter.ResponsesRequest{
		Model: "deepseek-chat",
		Input: []openrouter.ResponseItem{{Type: "message", Role: "user", Content: []openrouter.ContentPart{{Type: "input_text", Text: "hi"}}}},
	}
	openRouterCfg := &config.Config{}
	openRouterCfg.LLM.Provider = "openrouter"
	openRouterCfg.OpenRouter.BaseURL = "https://openrouter.ai/api/v1"
	deepSeekCfg := &config.Config{}
	deepSeekCfg.LLM.Provider = "deepseek"
	deepSeekCfg.DeepSeek.BaseURL = "https://api.deepseek.com"

	openRouterKey := (&Agent{cfg: openRouterCfg}).responsesCacheKey(req)
	deepSeekKey := (&Agent{cfg: deepSeekCfg}).responsesCacheKey(req)
	if openRouterKey == deepSeekKey {
		t.Fatalf("expected provider-specific cache keys to differ")
	}
}

func TestResponsesCacheKeyStableForSameProvider(t *testing.T) {
	req := openrouter.ResponsesRequest{
		Model: "deepseek-chat",
		Input: []openrouter.ResponseItem{{Type: "message", Role: "user", Content: []openrouter.ContentPart{{Type: "input_text", Text: "hi"}}}},
	}
	cfg := &config.Config{}
	cfg.LLM.Provider = "deepseek"
	cfg.DeepSeek.BaseURL = "https://api.deepseek.com"
	a := &Agent{cfg: cfg}

	if got, want := a.responsesCacheKey(req), a.responsesCacheKey(req); got != want {
		t.Fatalf("expected stable cache key, got %q want %q", got, want)
	}
}
