package tools

import (
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
	q = strings.ToLower(strings.TrimSpace(q))
	if q == "" {
		return r.List()
	}
	out := []ToolInfo{}
	for _, t := range r.specs {
		info := t.Info()
		if strings.Contains(strings.ToLower(info.Name), q) || strings.Contains(strings.ToLower(info.Description), q) {
			out = append(out, info)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
