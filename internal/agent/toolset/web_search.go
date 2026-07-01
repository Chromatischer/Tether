package toolset

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"tether/internal/tools"
)

type WebSearch struct{}

type webSearchArgs struct {
	Query string `json:"query"`
	Limit int    `json:"limit"`
}

func (t WebSearch) Spec() tools.ToolSpec {
	return tools.ToolSpec{
		Name:    "web-search",
		Summary: "Search the web and return a small list of results.",
		Safety:  "Network access. Only returns titles/URLs/snippets; does not fetch full pages.",
		InputSchema: map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"query": map[string]any{"type": "string", "minLength": 1, "description": "search query"},
				"limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 10, "description": "max results (default 5)"},
			},
			"required": []string{"query"},
		},
		OutputSchema: map[string]any{
			"type": "array",
			"items": map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]any{
					"title":   map[string]any{"type": "string"},
					"url":     map[string]any{"type": "string"},
					"snippet": map[string]any{"type": "string"},
				},
				"required": []string{"title", "url", "snippet"},
			},
		},
		Examples: []tools.ToolExample{
			{
				Title: "Search for a Go package",
				Args:  map[string]any{"query": "golang sqlite migrate library", "limit": 5},
				Result: []map[string]any{
					{"title": "...", "url": "https://...", "snippet": "..."},
				},
			},
		},
		Tags: []string{"web"},
	}
}

func (t WebSearch) Definition() ToolDef {
	spec := t.Spec()
	return ToolDef{Name: spec.Name, Description: tools.LLMDescription(spec), Parameters: spec.InputSchema}
}

type webResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
}

var (
	reResult  = regexp.MustCompile(`<a[^>]*class="result__a"[^>]*href="([^"]+)"[^>]*>(.*?)</a>`)
	reSnippet = regexp.MustCompile(`<a[^>]*class="result__snippet"[^>]*>(.*?)</a>`)
	reTags    = regexp.MustCompile(`<[^>]+>`)
)

func (t WebSearch) Execute(ctx context.Context, s *Session, rawArgs json.RawMessage) (any, error) {
	_ = s
	var args webSearchArgs
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return nil, err
	}
	q := strings.TrimSpace(args.Query)
	if q == "" {
		return nil, fmt.Errorf("query required")
	}
	limit := args.Limit
	if limit <= 0 {
		limit = 5
	}
	if limit > 10 {
		limit = 10
	}

	client := &http.Client{Timeout: 20 * time.Second}
	u := "https://duckduckgo.com/html/?q=" + url.QueryEscape(q)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	req.Header.Set("User-Agent", "Tether/0.1")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 300_000))
	html := string(b)

	matches := reResult.FindAllStringSubmatchIndex(html, limit)
	out := make([]webResult, 0, len(matches))
	for _, m := range matches {
		// indices: 0 full, 1 url, 2 title
		urlStr := html[m[2]:m[3]]
		titleHTML := html[m[4]:m[5]]
		title := strings.TrimSpace(reTags.ReplaceAllString(titleHTML, ""))

		// Find snippet after this match (best-effort, small window)
		windowEnd := m[1] + 2000
		if windowEnd > len(html) {
			windowEnd = len(html)
		}
		window := html[m[1]:windowEnd]
		snip := ""
		if sm := reSnippet.FindStringSubmatch(window); len(sm) == 2 {
			snip = strings.TrimSpace(reTags.ReplaceAllString(sm[1], ""))
		}

		out = append(out, webResult{Title: htmlUnescape(title), URL: urlStr, Snippet: htmlUnescape(snip)})
	}
	return out, nil
}

func htmlUnescape(s string) string {
	r := strings.NewReplacer(
		"&amp;", "&",
		"&lt;", "<",
		"&gt;", ">",
		"&quot;", "\"",
		"&#39;", "'",
	)
	return r.Replace(s)
}
