package tools

import (
	"sort"
	"strings"
)

type ToolInfo struct {
	Name        string
	Description string
}

type Registry struct {
	tools map[string]ToolInfo
}

func NewRegistry() *Registry {
	return &Registry{tools: map[string]ToolInfo{}}
}

func (r *Registry) Register(info ToolInfo) {
	if info.Name == "" {
		return
	}
	r.tools[info.Name] = info
}

func (r *Registry) List() []ToolInfo {
	out := make([]ToolInfo, 0, len(r.tools))
	for _, t := range r.tools {
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
	for _, t := range r.tools {
		if strings.Contains(strings.ToLower(t.Name), q) || strings.Contains(strings.ToLower(t.Description), q) {
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func DefaultRegistry() *Registry {
	r := NewRegistry()
	// Core minimal set. Note: tool execution is implemented later.
	r.Register(ToolInfo{Name: "bash", Description: "Run shell commands inside the user sandbox (no network)."})
	r.Register(ToolInfo{Name: "read", Description: "Read files inside the user sandbox."})
	r.Register(ToolInfo{Name: "write", Description: "Write files inside the user sandbox."})
	r.Register(ToolInfo{Name: "web-search", Description: "Search the web (network allowed)."})
	r.Register(ToolInfo{Name: "web-fetch", Description: "Fetch a URL (network allowed) and store response for summarization."})
	r.Register(ToolInfo{Name: "fetch.summarize", Description: "Summarize previously fetched content into safe markdown."})
	r.Register(ToolInfo{Name: "memory.list", Description: "List memory items (facts/prefs/tasks)."})
	r.Register(ToolInfo{Name: "memory.add", Description: "Add a memory item (fact/pref/task)."})
	r.Register(ToolInfo{Name: "memory.delete", Description: "Delete a memory item by id."})
	r.Register(ToolInfo{Name: "memory.update", Description: "Update a memory item by id."})
	// Note: secrets are managed via user commands (/secret ...). We intentionally do not expose
	// secret-management tools to the LLM to avoid accidental leakage.
	r.Register(ToolInfo{Name: "tool.search", Description: "Search for available tools (for on-demand tool activation)."})
	r.Register(ToolInfo{Name: "tool.enable", Description: "Enable a tool for the current agent session (reduces context size)."})
	r.Register(ToolInfo{Name: "confirm.request", Description: "Request user confirmation for destructive actions (scoped token)."})
	r.Register(ToolInfo{Name: "subagent.spawn", Description: "Spawn a sub-agent run (async)."})
	r.Register(ToolInfo{Name: "subagent.status", Description: "Get status/result of a sub-agent run."})
	r.Register(ToolInfo{Name: "proactive.run", Description: "Run a proactive rule check for the current user."})
	return r
}
