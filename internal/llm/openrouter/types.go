package openrouter

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ProviderPreferences are OpenRouter provider routing preferences.
// This is intentionally a small subset; extend as needed.
//
// OpenRouter provider routing and responses request schema.
//
// Note: OpenRouter expects provider slugs (e.g. "together", "deepinfra", "morph").
type ProviderPreferences struct {
	AllowFallbacks *bool    `json:"allow_fallbacks,omitempty"`
	Ignore         []string `json:"ignore,omitempty"`
	Only           []string `json:"only,omitempty"`
	Order          []string `json:"order,omitempty"`
}

// ErrorResponse is the standard OpenRouter error envelope.
// OpenRouter error response payload.
type ErrorResponse struct {
	Error struct {
		Code     any            `json:"code"`
		Message  string         `json:"message"`
		Metadata map[string]any `json:"metadata,omitempty"`
	} `json:"error"`

	UserID string `json:"user_id,omitempty"`
}

// HTTPError represents a non-2xx HTTP status returned by OpenRouter.
// It retains the raw response body (truncated by callers) for debugging.
type HTTPError struct {
	StatusCode int
	Body       []byte
	Parsed     *ErrorResponse
}

func (e *HTTPError) Error() string {
	// Preserve the historical error prefix so callers that match strings keep working.
	return fmt.Sprintf("openrouter status %d: %s", e.StatusCode, strings.TrimSpace(string(e.Body)))
}

func (e *HTTPError) Message() string {
	if e == nil || e.Parsed == nil {
		return ""
	}
	return strings.TrimSpace(e.Parsed.Error.Message)
}

func (e *HTTPError) ProviderName() string {
	if e == nil || e.Parsed == nil {
		return ""
	}
	if e.Parsed.Error.Metadata == nil {
		return ""
	}
	if v, ok := e.Parsed.Error.Metadata["provider_name"]; ok {
		if s, ok := v.(string); ok {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

func (e *HTTPError) RawUpstreamError() string {
	// OpenRouter often includes the upstream provider error in metadata.raw.
	if e == nil || e.Parsed == nil || e.Parsed.Error.Metadata == nil {
		return ""
	}
	raw, ok := e.Parsed.Error.Metadata["raw"]
	if !ok || raw == nil {
		return ""
	}
	// raw may be a string or an arbitrary object.
	switch v := raw.(type) {
	case string:
		return strings.TrimSpace(v)
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(b))
	}
}

func parseErrorResponse(body []byte) *ErrorResponse {
	var er ErrorResponse
	if err := json.Unmarshal(body, &er); err != nil {
		return nil
	}
	if strings.TrimSpace(er.Error.Message) == "" {
		return nil
	}
	return &er
}
