package tools

import (
	"regexp"
	"sort"
	"strings"
)

// Registry stores tool specs for discovery/documentation.
//
// The registry is used by:
//   - tool.search (LLM tool discovery)
//   - tool.describe (LLM tool docs)
//   - /tools list|search|describe (human-facing)
//
// Tool execution is implemented elsewhere; this is only metadata.
type Registry struct {
	specs map[string]ToolSpec
}

func NewRegistry() *Registry {
	return &Registry{specs: map[string]ToolSpec{}}
}

func (r *Registry) Register(spec ToolSpec) {
	name := strings.TrimSpace(spec.Name)
	if name == "" {
		return
	}
	spec.Name = name
	r.specs[name] = spec
}

func (r *Registry) Get(name string) (ToolSpec, bool) {
	s, ok := r.specs[strings.TrimSpace(name)]
	return s, ok
}

func (r *Registry) List() []ToolInfo {
	out := make([]ToolInfo, 0, len(r.specs))
	for _, t := range r.specs {
		out = append(out, t.Info())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (r *Registry) ListSpecs() []ToolSpec {
	out := make([]ToolSpec, 0, len(r.specs))
	for _, t := range r.specs {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (r *Registry) Search(q string) []ToolInfo {
	q = strings.TrimSpace(q)
	if q == "" {
		return r.List()
	}

	normalizedQuery := normalizeSearchText(q)
	queryTokens := searchTokens(q)
	if len(queryTokens) == 0 && normalizedQuery == "" {
		return r.List()
	}

	type scoredTool struct {
		info  ToolInfo
		score int
	}

	out := []scoredTool{}
	for _, t := range r.specs {
		info := t.Info()
		score := scoreToolSearch(t, normalizedQuery, queryTokens)
		if score > 0 {
			out = append(out, scoredTool{info: info, score: score})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].score != out[j].score {
			return out[i].score > out[j].score
		}
		return out[i].info.Name < out[j].info.Name
	})

	results := make([]ToolInfo, 0, len(out))
	for _, item := range out {
		results = append(results, item.info)
	}
	return results
}

var searchTokenPattern = regexp.MustCompile(`[a-z0-9]+`)

var searchSynonyms = map[string][]string{
	"bash":     {"shell", "command", "terminal", "cli"},
	"execute":  {"run"},
	"exec":     {"run"},
	"cmd":      {"command"},
	"cli":      {"shell", "command"},
	"terminal": {"shell", "command"},
}

func scoreToolSearch(spec ToolSpec, normalizedQuery string, queryTokens []string) int {
	name := normalizeSearchText(spec.Name)
	summary := normalizeSearchText(spec.Summary)
	safety := normalizeSearchText(spec.Safety)
	tags := normalizeSearchText(strings.Join(spec.Tags, " "))
	combined := strings.TrimSpace(strings.Join([]string{name, summary, safety, tags}, " "))

	if combined == "" {
		return 0
	}

	score := 0
	if normalizedQuery != "" {
		switch {
		case normalizedQuery == name:
			score += 200
		case strings.Contains(name, normalizedQuery):
			score += 120
		case strings.Contains(summary, normalizedQuery):
			score += 90
		case strings.Contains(combined, normalizedQuery):
			score += 70
		}
	}

	matchedTokens := 0
	for _, token := range expandSearchToken(queryTokens) {
		tokenScore := 0
		switch {
		case containsSearchToken(name, token):
			tokenScore = 50
		case containsSearchToken(tags, token):
			tokenScore = 35
		case containsSearchToken(summary, token):
			tokenScore = 25
		case containsSearchToken(safety, token):
			tokenScore = 10
		}
		if tokenScore > 0 {
			score += tokenScore
			matchedTokens++
		}
	}

	if len(queryTokens) > 1 && matchedTokens >= len(queryTokens) {
		score += 40
	}

	return score
}

func normalizeSearchText(s string) string {
	return strings.Join(searchTokenPattern.FindAllString(strings.ToLower(s), -1), " ")
}

func searchTokens(s string) []string {
	return searchTokenPattern.FindAllString(strings.ToLower(s), -1)
}

func containsSearchToken(text, token string) bool {
	if token == "" || text == "" {
		return false
	}
	return strings.Contains(" "+text+" ", " "+token+" ")
}

func expandSearchToken(tokens []string) []string {
	out := make([]string, 0, len(tokens)*2)
	seen := map[string]struct{}{}
	for _, token := range tokens {
		for _, candidate := range append([]string{token}, searchSynonyms[token]...) {
			if _, ok := seen[candidate]; ok || candidate == "" {
				continue
			}
			seen[candidate] = struct{}{}
			out = append(out, candidate)
		}
	}
	return out
}
