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

type ChatStreamChoiceDelta struct {
	Role             string     `json:"role,omitempty"`
	Content          string     `json:"content,omitempty"`
	ReasoningContent string     `json:"reasoning_content,omitempty"`
	ToolCalls        []ToolCall `json:"tool_calls,omitempty"`
}

type ChatStreamChoice struct {
	Index        int                   `json:"index"`
	Delta        ChatStreamChoiceDelta `json:"delta"`
	FinishReason string                `json:"finish_reason"`
}

type ChatStreamChunk struct {
	ID      string             `json:"id,omitempty"`
	Object  string             `json:"object,omitempty"`
	Created int64              `json:"created,omitempty"`
	Model   string             `json:"model,omitempty"`
	Choices []ChatStreamChoice `json:"choices"`
	Usage   *Usage             `json:"usage,omitempty"`
	Error   *struct {
		Code    any    `json:"code,omitempty"`
		Message string `json:"message,omitempty"`
	} `json:"error,omitempty"`
}

func (c *Client) ChatStream(ctx context.Context, req ChatRequest, onChunk func(ChatStreamChunk) error) (ChatResponse, error) {
	req.Stream = true
	if req.StreamOptions == nil {
		req.StreamOptions = &struct {
			IncludeUsage bool `json:"include_usage,omitempty"`
		}{IncludeUsage: true}
	}
	b, err := json.Marshal(req)
	if err != nil {
		return ChatResponse{}, err
	}
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.BaseURL, "/")+"/chat/completions", bytes.NewReader(b))
	if err != nil {
		return ChatResponse{}, err
	}
	hreq.Header.Set("Authorization", "Bearer "+c.APIKey)
	hreq.Header.Set("Content-Type", "application/json")
	hreq.Header.Set("Accept", "text/event-stream")
	if c.AppName != "" {
		hreq.Header.Set("X-Title", c.AppName)
	}

	resp, err := c.HTTP.Do(hreq)
	if err != nil {
		return ChatResponse{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
		return ChatResponse{}, &HTTPError{StatusCode: resp.StatusCode, Body: body, Parsed: parseErrorResponse(body)}
	}

	final := ChatResponse{}
	var content strings.Builder
	var reasoning strings.Builder
	toolCallsByIndex := map[int]ToolCall{}

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
		if line == "" || strings.HasPrefix(line, ":") || !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" {
			continue
		}
		if data == "[DONE]" {
			break
		}

		var chunk ChatStreamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}
		if chunk.Error != nil && strings.TrimSpace(chunk.Error.Message) != "" {
			return final, fmt.Errorf("chat stream error: %s", strings.TrimSpace(chunk.Error.Message))
		}
		if onChunk != nil {
			if err := onChunk(chunk); err != nil {
				return final, err
			}
		}
		if chunk.ID != "" {
			final.ID = chunk.ID
		}
		if chunk.Object != "" {
			final.Object = chunk.Object
		}
		if chunk.Created != 0 {
			final.Created = chunk.Created
		}
		if chunk.Model != "" {
			final.Model = chunk.Model
		}
		if chunk.Usage != nil {
			final.Usage = chunk.Usage
		}
		for _, choice := range chunk.Choices {
			if choice.Delta.Content != "" {
				content.WriteString(choice.Delta.Content)
			}
			if choice.Delta.ReasoningContent != "" {
				reasoning.WriteString(choice.Delta.ReasoningContent)
			}
			for _, tc := range choice.Delta.ToolCalls {
				idx := choice.Index
				if tc.Index != nil {
					idx = *tc.Index
				}
				if tc.ID == "" && tc.Type == "" && tc.Function.Name == "" && tc.Function.Arguments == "" {
					continue
				}
				existing := toolCallsByIndex[idx]
				if tc.ID != "" {
					existing.ID = tc.ID
				}
				if tc.Type != "" {
					existing.Type = tc.Type
				}
				if tc.Function.Name != "" {
					existing.Function.Name = tc.Function.Name
				}
				if tc.Function.Arguments != "" {
					existing.Function.Arguments += tc.Function.Arguments
				}
				toolCallsByIndex[idx] = existing
			}
		}
	}

	msg := Message{Role: "assistant", Content: Text(content.String()), ReasoningContent: reasoning.String()}
	if len(toolCallsByIndex) > 0 {
		for i := 0; ; i++ {
			tc, ok := toolCallsByIndex[i]
			if !ok {
				if i > len(toolCallsByIndex)+8 {
					break
				}
				continue
			}
			if tc.Type == "" {
				tc.Type = "function"
			}
			msg.ToolCalls = append(msg.ToolCalls, tc)
		}
	}
	final.Choices = []struct {
		Message      Message `json:"message"`
		FinishReason string  `json:"finish_reason"`
	}{{Message: msg, FinishReason: "stop"}}
	return final, nil
}
