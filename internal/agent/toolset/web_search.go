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
)

type WebSearch struct{}

type webSearchArgs struct {
	Query string `json:"query"`
	Limit int    `json:"limit"`
}

func (t WebSearch) Definition() ToolDef {
	return ToolDef{
		Name:        "web-search",
		Description: "Search the web (DuckDuckGo HTML) and return a small list of results.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{"type": "string"},
				"limit": map[string]any{"type": "integer", "description": "max results (default 5)"},
			},
			"required": []string{"query"},
		},
	}
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
