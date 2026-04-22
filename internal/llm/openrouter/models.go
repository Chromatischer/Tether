package openrouter

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

type ModelArchitecture struct {
	Modality         string   `json:"modality,omitempty"`
	InputModalities  []string `json:"input_modalities,omitempty"`
	OutputModalities []string `json:"output_modalities,omitempty"`
	Tokenizer        string   `json:"tokenizer,omitempty"`
}

type ModelPricing struct {
	Prompt         string `json:"prompt,omitempty"`
	Completion     string `json:"completion,omitempty"`
	InputCacheRead string `json:"input_cache_read,omitempty"`
	WebSearch      string `json:"web_search,omitempty"`
	Discount       int    `json:"discount,omitempty"`
}

type ModelTopProvider struct {
	ContextLength       int  `json:"context_length,omitempty"`
	MaxCompletionTokens int  `json:"max_completion_tokens,omitempty"`
	IsModerated         bool `json:"is_moderated,omitempty"`
}

type MetricValue struct {
	Number  *float64
	Summary string
}

func (m *MetricValue) UnmarshalJSON(data []byte) error {
	s := strings.TrimSpace(string(data))
	if s == "" || s == "null" {
		*m = MetricValue{}
		return nil
	}

	var num float64
	if err := json.Unmarshal(data, &num); err == nil {
		m.Number = &num
		m.Summary = ""
		return nil
	}

	var text string
	if err := json.Unmarshal(data, &text); err == nil {
		text = strings.TrimSpace(text)
		if text == "" {
			*m = MetricValue{}
			return nil
		}
		if parsed, err := strconv.ParseFloat(text, 64); err == nil {
			m.Number = &parsed
			m.Summary = ""
			return nil
		}
		m.Number = nil
		m.Summary = text
		return nil
	}

	var obj map[string]any
	if err := json.Unmarshal(data, &obj); err == nil {
		for _, key := range []string{"p50", "avg", "mean", "value"} {
			if v, ok := obj[key]; ok {
				switch vv := v.(type) {
				case float64:
					m.Number = &vv
					m.Summary = key
					return nil
				case string:
					vv = strings.TrimSpace(vv)
					if parsed, err := strconv.ParseFloat(vv, 64); err == nil {
						m.Number = &parsed
						m.Summary = key
						return nil
					}
				}
			}
		}
		compact, err := json.Marshal(obj)
		if err != nil {
			return nil
		}
		m.Number = nil
		m.Summary = string(compact)
		return nil
	}

	return fmt.Errorf("unsupported metric payload: %s", s)
}

type Model struct {
	ID                  string            `json:"id"`
	Name                string            `json:"name,omitempty"`
	Description         string            `json:"description,omitempty"`
	ContextLength       int               `json:"context_length,omitempty"`
	SupportedParameters []string          `json:"supported_parameters,omitempty"`
	Architecture        ModelArchitecture `json:"architecture,omitempty"`
	Pricing             ModelPricing      `json:"pricing,omitempty"`
	TopProvider         ModelTopProvider  `json:"top_provider,omitempty"`
}

type ModelEndpoint struct {
	Name                    string       `json:"name,omitempty"`
	ModelID                 string       `json:"model_id,omitempty"`
	ModelName               string       `json:"model_name,omitempty"`
	ContextLength           int          `json:"context_length,omitempty"`
	Pricing                 ModelPricing `json:"pricing,omitempty"`
	ProviderName            string       `json:"provider_name,omitempty"`
	Tag                     string       `json:"tag,omitempty"`
	Quantization            string       `json:"quantization,omitempty"`
	MaxCompletionTokens     int          `json:"max_completion_tokens,omitempty"`
	MaxPromptTokens         int          `json:"max_prompt_tokens,omitempty"`
	SupportedParameters     []string     `json:"supported_parameters,omitempty"`
	Status                  int          `json:"status,omitempty"`
	UptimeLast30M           float64      `json:"uptime_last_30m,omitempty"`
	UptimeLast5M            float64      `json:"uptime_last_5m,omitempty"`
	UptimeLast1D            float64      `json:"uptime_last_1d,omitempty"`
	SupportsImplicitCaching bool         `json:"supports_implicit_caching,omitempty"`
	LatencyLast30M          MetricValue  `json:"latency_last_30m,omitempty"`
	ThroughputLast30M       MetricValue  `json:"throughput_last_30m,omitempty"`
}

type ModelsResponse struct {
	Data []Model `json:"data"`
}

type ModelEndpointsResponse struct {
	Data struct {
		ID        string          `json:"id"`
		Name      string          `json:"name"`
		Endpoints []ModelEndpoint `json:"endpoints"`
	} `json:"data"`
}

func (c *Client) Models(ctx context.Context) ([]Model, error) {
	hreq, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.BaseURL, "/")+"/models", nil)
	if err != nil {
		return nil, err
	}
	hreq.Header.Set("Authorization", "Bearer "+c.APIKey)
	if c.AppName != "" {
		hreq.Header.Set("X-Title", c.AppName)
	}

	resp, err := c.HTTP.Do(hreq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &HTTPError{StatusCode: resp.StatusCode, Body: body, Parsed: parseErrorResponse(body)}
	}

	var out ModelsResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	return out.Data, nil
}

func (c *Client) ModelEndpoints(ctx context.Context, modelID string) ([]ModelEndpoint, error) {
	modelID = strings.TrimSpace(modelID)
	hreq, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.BaseURL, "/")+"/models/"+modelID+"/endpoints", nil)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(c.APIKey) != "" {
		hreq.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	if c.AppName != "" {
		hreq.Header.Set("X-Title", c.AppName)
	}

	resp, err := c.HTTP.Do(hreq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &HTTPError{StatusCode: resp.StatusCode, Body: body, Parsed: parseErrorResponse(body)}
	}

	var out ModelEndpointsResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	return out.Data.Endpoints, nil
}
