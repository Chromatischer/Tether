package openrouter

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Responses API (OpenAI-compatible, OpenRouter beta)
// BaseURL: https://openrouter.ai/api/v1
// Endpoint: POST {BaseURL}/responses

// ResponsesRequest is a subset of the OpenAI/OpenRouter Responses API request.
// See: https://openrouter.ai/docs/api/reference/responses/overview
type ResponsesRequest struct {
	Model string `json:"model"`

	// Input can be a string or an array of items (messages, function_call, ...)
	Input any `json:"input"`

	Stream          bool    `json:"stream,omitempty"`
	MaxOutputTokens int     `json:"max_output_tokens,omitempty"`
	Temperature     float64 `json:"temperature,omitempty"`
	TopP            float64 `json:"top_p,omitempty"`

	Tools      []ResponsesTool `json:"tools,omitempty"`
	ToolChoice any             `json:"tool_choice,omitempty"`
}

type ResponsesTool struct {
	Type        string      `json:"type"` // "function"
	Name        string      `json:"name"`
	Description string      `json:"description,omitempty"`
	Strict      any         `json:"strict,omitempty"` // docs show null
	Parameters  interface{} `json:"parameters,omitempty"`
}

// ResponseItem represents a Responses API input/output item.
// It is intentionally permissive: different item types use different fields.
// Known types we use: message, function_call, function_call_output.
//
// Docs: https://openrouter.ai/docs/api/reference/responses/basic-usage
//
//	https://openrouter.ai/docs/api/reference/responses/tool-calling
type ResponseItem struct {
	Type   string `json:"type"`
	ID     string `json:"id,omitempty"`
	Status string `json:"status,omitempty"`

	// message
	Role    string        `json:"role,omitempty"` // system|user|assistant
	Content []ContentPart `json:"content,omitempty"`

	// function_call
	CallID    string `json:"call_id,omitempty"`
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"` // JSON string

	// function_call_output
	Output string `json:"output,omitempty"` // JSON string
}

type ContentPart struct {
	Type        string `json:"type"` // input_text|output_text
	Text        string `json:"text,omitempty"`
	Annotations any    `json:"annotations,omitempty"`
}

type ResponsesUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

type ResponsesResponse struct {
	ID        string          `json:"id"`
	Object    string          `json:"object"`
	CreatedAt int64           `json:"created_at,omitempty"`
	Model     string          `json:"model,omitempty"`
	Output    []ResponseItem  `json:"output,omitempty"`
	Usage     *ResponsesUsage `json:"usage,omitempty"`
	Status    string          `json:"status,omitempty"`

	// OpenRouter may return a top-level error event mid-stream on other endpoints.
	// For Responses API errors before streaming starts, the HTTP status is non-2xx.
	Error *struct {
		Code    any    `json:"code,omitempty"`
		Message string `json:"message,omitempty"`
	} `json:"error,omitempty"`
}

// ResponsesStreamEvent is an SSE "data:" payload for /responses streaming.
// We parse only the fields we need; everything else is ignored.
// Example events are in the docs:
// https://openrouter.ai/docs/api/reference/responses/basic-usage
// https://openrouter.ai/docs/api/reference/responses/tool-calling
type ResponsesStreamEvent struct {
	Type string `json:"type"`

	ResponseID   string `json:"response_id,omitempty"`
	OutputIndex  *int   `json:"output_index,omitempty"`
	ContentIndex *int   `json:"content_index,omitempty"`

	Delta     string `json:"delta,omitempty"`
	Arguments string `json:"arguments,omitempty"`

	Item     *ResponseItem      `json:"item,omitempty"`
	Part     *ContentPart       `json:"part,omitempty"`
	Response *ResponsesResponse `json:"response,omitempty"`
}

func (c *Client) Responses(ctx context.Context, req ResponsesRequest) (ResponsesResponse, error) {
	b, err := json.Marshal(req)
	if err != nil {
		return ResponsesResponse{}, err
	}
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/responses", bytes.NewReader(b))
	if err != nil {
		return ResponsesResponse{}, err
	}
	hreq.Header.Set("Authorization", "Bearer "+c.APIKey)
	hreq.Header.Set("Content-Type", "application/json")
	if c.AppName != "" {
		hreq.Header.Set("X-Title", c.AppName)
	}

	resp, err := c.HTTP.Do(hreq)
	if err != nil {
		return ResponsesResponse{}, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ResponsesResponse{}, fmt.Errorf("openrouter status %d: %s", resp.StatusCode, string(body))
	}

	var out ResponsesResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return ResponsesResponse{}, err
	}
	return out, nil
}

// ResponsesStream executes a streaming /responses request (SSE) and calls onEvent for each event.
// It returns the final response payload from the response.done event.
func (c *Client) ResponsesStream(ctx context.Context, req ResponsesRequest, onEvent func(ev ResponsesStreamEvent) error) (ResponsesResponse, error) {
	req.Stream = true
	b, err := json.Marshal(req)
	if err != nil {
		return ResponsesResponse{}, err
	}
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/responses", bytes.NewReader(b))
	if err != nil {
		return ResponsesResponse{}, err
	}
	hreq.Header.Set("Authorization", "Bearer "+c.APIKey)
	hreq.Header.Set("Content-Type", "application/json")
	hreq.Header.Set("Accept", "text/event-stream")
	if c.AppName != "" {
		hreq.Header.Set("X-Title", c.AppName)
	}

	resp, err := c.HTTP.Do(hreq)
	if err != nil {
		return ResponsesResponse{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
		return ResponsesResponse{}, fmt.Errorf("openrouter status %d: %s", resp.StatusCode, string(body))
	}

	final := ResponsesResponse{}
	r := bufio.NewReader(resp.Body)
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				break
			}
			return final, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			continue
		}
		// SSE keepalive comment
		if strings.HasPrefix(line, ":") {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" {
			continue
		}
		if data == "[DONE]" {
			break
		}

		var ev ResponsesStreamEvent
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			// Ignore invalid payloads (some proxies may send non-JSON).
			continue
		}

		if ev.Response != nil && ev.Response.Error != nil {
			return final, fmt.Errorf("openrouter stream error: %s", strings.TrimSpace(ev.Response.Error.Message))
		}

		if onEvent != nil {
			if err := onEvent(ev); err != nil {
				return final, err
			}
		}

		if ev.Type == "response.done" && ev.Response != nil {
			final = *ev.Response
		}
	}
	return final, nil
}
