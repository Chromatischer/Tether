package openrouter

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
)

// Responses API (OpenAI-compatible, OpenRouter beta)
// BaseURL: https://openrouter.ai/api/v1
// Endpoint: POST {BaseURL}/responses

// ResponsesRequest is a subset of the OpenAI/OpenRouter Responses API request.
// See: https://openrouter.ai/docs/api/reference/responses/overview
type ResponsesRequest struct {
	Model string `json:"model"`

	// Models enables OpenRouter model fallbacks (try these in order if the primary fails).
	// Docs: https://openrouter.ai/docs/guides/routing/model-fallbacks
	Models []string `json:"models,omitempty"`

	// Input can be a string or an array of items (messages, function_call, ...)
	Input any `json:"input"`

	Stream          bool    `json:"stream,omitempty"`
	MaxOutputTokens int     `json:"max_output_tokens,omitempty"`
	Temperature     float64 `json:"temperature,omitempty"`
	TopP            float64 `json:"top_p,omitempty"`

	Tools      []ResponsesTool     `json:"tools,omitempty"`
	ToolChoice any                 `json:"tool_choice,omitempty"`
	Reasoning  *ResponsesReasoning `json:"reasoning,omitempty"`

	// Include requests extra fields on output items. We use
	// "reasoning.encrypted_content" so providers (Anthropic, OpenAI o-series)
	// return the signed/encrypted reasoning payload, which can then be replayed
	// in follow-up turns without tripping signature validation.
	// Docs: https://openrouter.ai/docs/api/reference/responses/overview
	Include []string `json:"include,omitempty"`

	// Provider routing preferences (OpenRouter-specific).
	Provider *ProviderPreferences `json:"provider,omitempty"`
}

type ResponsesReasoning struct {
	// Effort pins the reasoning depth (low|medium|high). Left empty for
	// adaptive reasoning, where Enabled is set instead and the model decides
	// how much to spend.
	Effort string `json:"effort,omitempty"`
	// Enabled turns reasoning on without pinning an effort. Pointer so the
	// field is omitted unless explicitly set.
	Enabled *bool `json:"enabled,omitempty"`
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

	// reasoning
	EncryptedContent string                 `json:"encrypted_content,omitempty"`
	Summary          []ReasoningSummaryPart `json:"summary,omitempty"`
}

type ReasoningSummaryPart struct {
	Text string
}

// MarshalJSON emits the Responses API summary-part shape
// ({"type":"summary_text","text":"..."}) so a reasoning item read from one turn
// can be replayed as input on the next without being mangled into {"Text":"..."}.
func (p ReasoningSummaryPart) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}{Type: "summary_text", Text: p.Text})
}

func (p *ReasoningSummaryPart) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		p.Text = s
		return nil
	}

	var obj struct {
		Text    string `json:"text"`
		Summary string `json:"summary"`
		Value   string `json:"value"`
	}
	if err := json.Unmarshal(data, &obj); err != nil {
		return nil
	}
	switch {
	case strings.TrimSpace(obj.Text) != "":
		p.Text = obj.Text
	case strings.TrimSpace(obj.Summary) != "":
		p.Text = obj.Summary
	case strings.TrimSpace(obj.Value) != "":
		p.Text = obj.Value
	}
	return nil
}

type ContentPart struct {
	Type        string `json:"type"` // input_text|output_text
	Text        string `json:"text,omitempty"`
	Annotations any    `json:"annotations,omitempty"`
}

type ResponsesUsage struct {
	InputTokens  int     `json:"input_tokens"`
	OutputTokens int     `json:"output_tokens"`
	TotalTokens  int     `json:"total_tokens"`
	Cost         float64 `json:"cost,omitempty"`
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
	Error    *StreamError       `json:"error,omitempty"`
	Message  string             `json:"message,omitempty"`
	Reason   string             `json:"reason,omitempty"`
	Code     any                `json:"code,omitempty"`
}

type StreamError struct {
	Code     any            `json:"code,omitempty"`
	Message  string         `json:"message,omitempty"`
	Reason   string         `json:"reason,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
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
		return ResponsesResponse{}, &HTTPError{StatusCode: resp.StatusCode, Body: body, Parsed: parseErrorResponse(body)}
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
		return ResponsesResponse{}, &HTTPError{StatusCode: resp.StatusCode, Body: body, Parsed: parseErrorResponse(body)}
	}

	final := ResponsesResponse{}
	outputsByIndex := map[int]ResponseItem{}
	functionCallArgsByIndex := map[int]string{}
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

		if msg := streamErrorMessage(ev); msg != "" {
			return final, fmt.Errorf("openrouter stream error: %s", msg)
		}

		if onEvent != nil {
			if err := onEvent(ev); err != nil {
				return final, err
			}
		}

		switch ev.Type {
		case "response.output_item.added", "response.output_item.done":
			if ev.Item != nil && ev.OutputIndex != nil {
				item := *ev.Item
				if item.Type == "function_call" {
					if args := strings.TrimSpace(functionCallArgsByIndex[*ev.OutputIndex]); len(args) >= len(strings.TrimSpace(item.Arguments)) {
						item.Arguments = args
					}
				}
				outputsByIndex[*ev.OutputIndex] = item
			}
		case "response.function_call_arguments.delta":
			if ev.OutputIndex != nil {
				delta := ev.Delta
				if delta == "" {
					delta = ev.Arguments
				}
				if delta != "" {
					functionCallArgsByIndex[*ev.OutputIndex] += delta
					if item, ok := outputsByIndex[*ev.OutputIndex]; ok && item.Type == "function_call" {
						item.Arguments = functionCallArgsByIndex[*ev.OutputIndex]
						outputsByIndex[*ev.OutputIndex] = item
					}
				}
			}
		case "response.function_call_arguments.done":
			if ev.OutputIndex != nil {
				args := ev.Arguments
				if args == "" {
					args = ev.Delta
				}
				if args == "" {
					args = functionCallArgsByIndex[*ev.OutputIndex]
				}
				if args != "" {
					functionCallArgsByIndex[*ev.OutputIndex] = args
					if item, ok := outputsByIndex[*ev.OutputIndex]; ok && item.Type == "function_call" {
						item.Arguments = args
						outputsByIndex[*ev.OutputIndex] = item
					}
				}
			}
		}

		if ev.Type == "response.done" && ev.Response != nil {
			final = *ev.Response
		}
	}
	if len(final.Output) > 0 && len(outputsByIndex) > 0 {
		for i := range final.Output {
			if cached, ok := outputsByIndex[i]; ok {
				final.Output[i] = mergeResponseItem(final.Output[i], cached)
			}
		}
	}
	if len(final.Output) == 0 && len(outputsByIndex) > 0 {
		indexes := make([]int, 0, len(outputsByIndex))
		for idx := range outputsByIndex {
			indexes = append(indexes, idx)
		}
		sort.Ints(indexes)
		final.Output = make([]ResponseItem, 0, len(indexes))
		for _, idx := range indexes {
			final.Output = append(final.Output, outputsByIndex[idx])
		}
	}
	return final, nil
}

func mergeResponseItem(current, cached ResponseItem) ResponseItem {
	if current.Type == "" {
		current.Type = cached.Type
	}
	if current.ID == "" {
		current.ID = cached.ID
	}
	if current.Status == "" {
		current.Status = cached.Status
	}
	if current.Role == "" {
		current.Role = cached.Role
	}
	if len(current.Content) == 0 && len(cached.Content) > 0 {
		current.Content = cached.Content
	}
	if current.CallID == "" {
		current.CallID = cached.CallID
	}
	if current.Name == "" {
		current.Name = cached.Name
	}
	if len(strings.TrimSpace(cached.Arguments)) > len(strings.TrimSpace(current.Arguments)) {
		current.Arguments = cached.Arguments
	}
	if current.Output == "" {
		current.Output = cached.Output
	}
	if current.EncryptedContent == "" {
		current.EncryptedContent = cached.EncryptedContent
	}
	if len(current.Summary) == 0 && len(cached.Summary) > 0 {
		current.Summary = cached.Summary
	}
	return current
}

func streamErrorMessage(ev ResponsesStreamEvent) string {
	if ev.Response != nil && ev.Response.Error != nil {
		if msg := strings.TrimSpace(ev.Response.Error.Message); msg != "" {
			return msg
		}
	}
	if ev.Error != nil {
		if msg := strings.TrimSpace(ev.Error.Message); msg != "" {
			return msg
		}
		if reason := strings.TrimSpace(ev.Error.Reason); reason != "" {
			return reason
		}
	}
	if msg := strings.TrimSpace(ev.Message); msg != "" {
		return msg
	}
	if reason := strings.TrimSpace(ev.Reason); reason != "" {
		return "stream closed with reason: " + reason
	}
	if ev.Type == "error" || strings.HasSuffix(ev.Type, ".error") {
		return "stream closed with reason: error"
	}
	return ""
}
