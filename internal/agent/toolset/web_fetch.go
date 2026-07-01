package toolset

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"tether/internal/redact"
	"tether/internal/store"
	"tether/internal/tools"
)

type WebFetch struct{}

type webFetchArgs struct {
	URL             string            `json:"url"`
	MaxBytes        int               `json:"max_bytes"`
	CacheTTLSeconds *int              `json:"cache_ttl_seconds"`
	Headers         map[string]string `json:"headers"`
	SecretHeaders   map[string]string `json:"secret_headers"`
	ConfirmToken    string            `json:"confirm_token"`
	ReturnBody      bool              `json:"return_body"`
}

func (t WebFetch) Spec() tools.ToolSpec {
	return tools.ToolSpec{
		Name:    "web-fetch",
		Summary: "Fetch a URL over the network and cache the truncated response body.",
		Safety: "Network access. Authenticated fetches (secret_headers) and returning raw body require confirm_token. " +
			"Private, loopback, and link-local addresses are blocked by default (SSRF protection).",
		InputSchema: map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"url": map[string]any{"type": "string", "minLength": 1, "description": "http(s) URL"},
				"max_bytes": map[string]any{
					"type":        "integer",
					"minimum":     1,
					"maximum":     200000,
					"description": "max bytes to read (default 50000, max 200000)",
				},
				"cache_ttl_seconds": map[string]any{
					"type":        "integer",
					"minimum":     0,
					"maximum":     86400,
					"description": "cache TTL (default 3600). set 0 to disable.",
				},
				"headers": map[string]any{
					"type":                 "object",
					"additionalProperties": map[string]any{"type": "string"},
					"description":          "plain headers",
				},
				"secret_headers": map[string]any{
					"type":                 "object",
					"additionalProperties": map[string]any{"type": "string"},
					"description":          "headerName -> secret label (resolved locally). Requires confirm_token.",
				},
				"confirm_token": map[string]any{
					"type":        "string",
					"description": "required for authenticated fetches and raw body returns",
				},
				"return_body": map[string]any{
					"type":        "boolean",
					"description": "return raw body to the model (discouraged); requires confirm_token",
				},
			},
			"required": []string{"url"},
		},
		OutputSchema: map[string]any{
			"type":                 "object",
			"additionalProperties": true,
			"properties": map[string]any{
				"fetch_id":       map[string]any{"type": "string"},
				"url":            map[string]any{"type": "string"},
				"status":         map[string]any{"type": "integer"},
				"contentType":    map[string]any{"type": "string"},
				"truncated":      map[string]any{"type": "boolean"},
				"bytes":          map[string]any{"type": "integer"},
				"cache_hit":      map[string]any{"type": "boolean"},
				"preview":        map[string]any{"type": "string"},
				"fetched_at_utc": map[string]any{"type": "string"},
				"body":           map[string]any{"type": "string", "description": "only present when return_body=true"},
			},
			"required": []string{"fetch_id", "url", "status", "contentType", "truncated", "bytes", "cache_hit", "preview", "fetched_at_utc"},
		},
		Examples: []tools.ToolExample{
			{
				Title: "Fetch a page then summarize",
				Args:  map[string]any{"url": "https://example.com", "max_bytes": 50000},
				Result: map[string]any{
					"fetch_id":       "...",
					"url":            "https://example.com",
					"status":         200,
					"contentType":    "text/html; charset=UTF-8",
					"truncated":      false,
					"bytes":          12345,
					"cache_hit":      false,
					"preview":        "...",
					"fetched_at_utc": "2026-01-02T03:04:05Z",
				},
				Notes: "Then call fetch.summarize with the returned fetch_id.",
			},
		},
		Tags: []string{"web", "cache"},
	}
}

func (t WebFetch) Definition() ToolDef {
	spec := t.Spec()
	return ToolDef{Name: spec.Name, Description: tools.LLMDescription(spec), Parameters: spec.InputSchema}
}

func (t WebFetch) Execute(ctx context.Context, s *Session, rawArgs json.RawMessage) (any, error) {
	var args webFetchArgs
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return nil, err
	}
	u := strings.TrimSpace(args.URL)
	if u == "" {
		return nil, fmt.Errorf("url required")
	}
	parsed, err := url.Parse(u)
	if err != nil {
		return nil, err
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("unsupported scheme: %s", parsed.Scheme)
	}

	max := args.MaxBytes
	if max <= 0 {
		max = 50_000
	}
	if max > 200_000 {
		max = 200_000
	}

	ttl := 3600
	if args.CacheTTLSeconds != nil {
		ttl = *args.CacheTTLSeconds
	}
	if ttl < 0 {
		ttl = 0
	}
	// ttl==0 disables cache.
	if ttl > 0 && ttl < 60 {
		ttl = 60
	}
	if ttl > 24*60*60 {
		ttl = 24 * 60 * 60
	}

	if s.DB == nil {
		return nil, fmt.Errorf("db not configured")
	}

	// Authenticated fetches (using secret headers) and raw-body returns require explicit confirmation.
	// Confirm tokens are single-use, so we consume at most once with a scope that covers the full action.
	needsConfirm := len(args.SecretHeaders) > 0 || args.ReturnBody
	cacheKey := webFetchCacheKey(u, max, args.Headers, args.SecretHeaders)
	if needsConfirm {
		scope := webFetchConfirmScope(cacheKey, u, len(args.SecretHeaders) > 0, args.ReturnBody)
		if s.Confirm == nil || !s.Confirm.Consume(s.UserID, strings.TrimSpace(args.ConfirmToken), scope) {
			return nil, fmt.Errorf("web-fetch requires confirmation; scope=%q", scope)
		}
	}

	if ttl > 0 {
		if cached, ok, err := store.GetWebFetchCache(s.DB, s.UserID, cacheKey); err == nil && ok {
			if !cached.FetchedAt.IsZero() && time.Since(cached.FetchedAt) < time.Duration(ttl)*time.Second {
				preview := truncateForPreview(string(cached.Body), 1500)
				preview, _ = redact.ScanAndRedact(preview)
				out := map[string]any{
					"fetch_id":       cacheKey,
					"url":            cached.URL,
					"status":         cached.Status,
					"contentType":    cached.ContentType,
					"truncated":      cached.Truncated,
					"bytes":          cached.Bytes,
					"cache_hit":      true,
					"preview":        preview,
					"fetched_at_utc": cached.FetchedAt.UTC().Format(time.RFC3339),
				}
				if args.ReturnBody {
					body := string(cached.Body)
					body, _ = redact.ScanAndRedact(body)
					out["body"] = body
				}
				return out, nil
			}
		}
	}

	client := safeFetchHTTPClient(30*time.Second, s.AllowPrivateNetworkFetch)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Tether/0.1")
	for k, v := range args.Headers {
		if strings.TrimSpace(k) == "" {
			continue
		}
		req.Header.Set(k, v)
	}
	for headerName, secretLabel := range args.SecretHeaders {
		if strings.TrimSpace(headerName) == "" {
			continue
		}
		if s.Secrets == nil {
			return nil, fmt.Errorf("secret_headers provided but secrets store not configured")
		}
		sec, ok, err := s.Secrets.Get(ctx, s.UserID, secretLabel)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, fmt.Errorf("secret not found: %s", secretLabel)
		}
		req.Header.Set(headerName, sec)
	}
	resp, err := client.Do(req)
	if err != nil {
		var blocked *blockedAddrError
		if errors.As(err, &blocked) {
			return nil, fmt.Errorf(
				"web-fetch blocked: %q resolves to internal address %s. "+
					"Fetching private, loopback, or link-local addresses is disabled to prevent SSRF. "+
					"This is not a transient error — retrying the same URL will fail again. "+
					"If this endpoint is genuinely intended and authorized, an admin can enable "+
					"private-network fetches in the admin 'agent' tab.",
				u, blocked.ip,
			)
		}
		return nil, err
	}
	defer resp.Body.Close()

	b, _ := io.ReadAll(io.LimitReader(resp.Body, int64(max)))
	truncated := false
	if len(b) >= max {
		truncated = true
	}

	ct := resp.Header.Get("Content-Type")
	_ = store.UpsertWebFetchCache(s.DB, store.WebFetchCacheEntry{
		UserID:      s.UserID,
		CacheKey:    cacheKey,
		URL:         u,
		Status:      resp.StatusCode,
		ContentType: ct,
		Body:        b,
		Truncated:   truncated,
		Bytes:       len(b),
	})

	preview := truncateForPreview(string(b), 1500)
	preview, _ = redact.ScanAndRedact(preview)

	out := map[string]any{
		"fetch_id":       cacheKey,
		"url":            u,
		"status":         resp.StatusCode,
		"contentType":    ct,
		"truncated":      truncated,
		"bytes":          len(b),
		"cache_hit":      false,
		"preview":        preview,
		"fetched_at_utc": time.Now().UTC().Format(time.RFC3339),
	}
	if args.ReturnBody {
		body := string(b)
		body, _ = redact.ScanAndRedact(body)
		out["body"] = body
	}
	return out, nil
}

func webFetchCacheKey(url string, maxBytes int, headers map[string]string, secretHeaders map[string]string) string {
	pairs := make([]string, 0, len(headers)+len(secretHeaders)+2)
	pairs = append(pairs, "url="+url)
	pairs = append(pairs, fmt.Sprintf("max=%d", maxBytes))
	for k, v := range headers {
		pairs = append(pairs, "h:"+strings.ToLower(strings.TrimSpace(k))+"="+strings.TrimSpace(v))
	}
	for k, v := range secretHeaders {
		// include only label reference, not secret value
		pairs = append(pairs, "sh:"+strings.ToLower(strings.TrimSpace(k))+"="+strings.TrimSpace(v))
	}
	sort.Strings(pairs)
	s := strings.Join(pairs, "\n")
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func webFetchConfirmScope(cacheKey string, u string, usesSecrets bool, returnBody bool) string {
	flag := "plain"
	switch {
	case usesSecrets && returnBody:
		flag = "auth+return_body"
	case usesSecrets:
		flag = "auth"
	case returnBody:
		flag = "return_body"
	}
	preview := strings.TrimSpace(u)
	if len(preview) > 90 {
		preview = preview[:90] + "…"
	}
	return "web-fetch:" + cacheKey + ":" + flag + ":" + preview
}

func truncateForPreview(s string, max int) string {
	s = strings.TrimSpace(s)
	if max <= 0 {
		max = 1000
	}
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
