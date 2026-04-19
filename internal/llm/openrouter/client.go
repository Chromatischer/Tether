package openrouter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

type Client struct {
	BaseURL string
	APIKey  string
	HTTP    *http.Client
	AppName string
}

type Message struct {
	Role    string  `json:"role"`
	Content *string `json:"content"`

	// Tool calling
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	Name       string     `json:"name,omitempty"` // optional; some SDKs include it for tool messages
}

type Tool struct {
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}

type ToolFunction struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Parameters  interface{} `json:"parameters"`
}

type ToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function ToolCallFunction `json:"function"`
}

type ToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // JSON string
}

type ChatRequest struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Temperature float64   `json:"temperature,omitempty"`
	MaxTokens   int       `json:"max_tokens,omitempty"`

	Tools             []Tool      `json:"tools,omitempty"`
	ToolChoice        interface{} `json:"tool_choice,omitempty"`
	ParallelToolCalls bool        `json:"parallel_tool_calls"`
}

type Usage struct {
	PromptTokens     int     `json:"prompt_tokens"`
	CompletionTokens int     `json:"completion_tokens"`
	TotalTokens      int     `json:"total_tokens"`
	Cost             float64 `json:"cost,omitempty"`

	PromptTokensDetails *struct {
		CachedTokens     int `json:"cached_tokens,omitempty"`
		CacheWriteTokens int `json:"cache_write_tokens,omitempty"`
	} `json:"prompt_tokens_details,omitempty"`
}

type ChatResponse struct {
	Choices []struct {
		Message      Message `json:"message"`
		FinishReason string  `json:"finish_reason"`
	} `json:"choices"`

	Usage *Usage `json:"usage,omitempty"`
}

func New(baseURL, apiKey, appName string) *Client {
	if baseURL == "" {
		baseURL = "https://openrouter.ai/api/v1"
	}
	return &Client{
		BaseURL: baseURL,
		APIKey:  apiKey,
		AppName: appName,
		// Use context deadlines for timeouts; streaming (SSE) requires no fixed client timeout.
		HTTP: &http.Client{Timeout: 0},
	}
}

func Text(s string) *string { return &s }

func (c *Client) Chat(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	b, err := json.Marshal(req)
	if err != nil {
		return ChatResponse{}, err
	}
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/chat/completions", bytes.NewReader(b))
	if err != nil {
		return ChatResponse{}, err
	}
	hreq.Header.Set("Authorization", "Bearer "+c.APIKey)
	hreq.Header.Set("Content-Type", "application/json")
	if c.AppName != "" {
		hreq.Header.Set("X-Title", c.AppName)
	}

	resp, err := c.HTTP.Do(hreq)
	if err != nil {
		return ChatResponse{}, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ChatResponse{}, &HTTPError{StatusCode: resp.StatusCode, Body: body, Parsed: parseErrorResponse(body)}
	}

	var out ChatResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return ChatResponse{}, err
	}
	if len(out.Choices) == 0 {
		return ChatResponse{}, fmt.Errorf("openrouter: no choices")
	}
	return out, nil
}
