package deepseek

import (
	"context"

	"tether/internal/llm/openrouter"
)

type Client struct {
	*openrouter.Client
}

func New(baseURL, apiKey, appName string) *Client {
	if baseURL == "" {
		baseURL = "https://api.deepseek.com"
	}
	return &Client{Client: openrouter.New(baseURL, apiKey, appName)}
}

func (c *Client) Responses(ctx context.Context, req openrouter.ResponsesRequest) (openrouter.ResponsesResponse, error) {
	return c.Client.ResponsesViaChat(ctx, req)
}

func (c *Client) ResponsesStream(ctx context.Context, req openrouter.ResponsesRequest, onEvent func(openrouter.ResponsesStreamEvent) error) (openrouter.ResponsesResponse, error) {
	return c.Client.ResponsesStreamViaChat(ctx, req, onEvent)
}

func (c *Client) Models(ctx context.Context) ([]openrouter.Model, error) {
	models, err := c.Client.Models(ctx)
	if err == nil {
		return models, nil
	}
	return []openrouter.Model{
		{ID: "deepseek-v4-flash", Name: "DeepSeek V4 Flash", ContextLength: 128000, Architecture: openrouter.ModelArchitecture{Tokenizer: "deepseek"}},
		{ID: "deepseek-v4-pro", Name: "DeepSeek V4 Pro", ContextLength: 128000, Architecture: openrouter.ModelArchitecture{Tokenizer: "deepseek"}},
		{ID: "deepseek-chat", Name: "DeepSeek Chat", ContextLength: 128000, Architecture: openrouter.ModelArchitecture{Tokenizer: "deepseek"}},
		{ID: "deepseek-reasoner", Name: "DeepSeek Reasoner", ContextLength: 128000, Architecture: openrouter.ModelArchitecture{Tokenizer: "deepseek"}},
	}, nil
}
