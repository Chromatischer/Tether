package tools

import "sort"

// Tool categories. Tools are enabled/disabled by category (group), never one at
// a time. Every tool spec must declare exactly one of these as its Category.
const (
	CategoryTools      = "tools"      // tool management meta-tools (always on)
	CategoryConfirm    = "confirm"    // confirmation helpers (always on)
	CategoryFiles      = "files"      // read/write the sandbox filesystem
	CategoryWeb        = "web"        // web search/fetch/summarize
	CategoryMemory     = "memory"     // long-term memory items
	CategorySubagents  = "subagents"  // spawn and inspect sub-agents
	CategorySkills     = "skills"     // invoke skills/playbooks
	CategoryScheduling = "scheduling" // proactive runs and self-scheduling
	CategoryExec       = "exec"       // shell execution (bash)
	CategoryMCP        = "mcp"        // external Model Context Protocol tools
	CategoryAdmin      = "admin"      // privileged, unsandboxed host execution
)

// CategoryMeta describes a category for discovery and documentation.
type CategoryMeta struct {
	Name string `json:"name"`
	// Description is a short, human/LLM-facing explanation of the group.
	Description string `json:"description"`
	// AlwaysOn categories cannot be disabled and are active in every session.
	AlwaysOn bool `json:"always_on"`
	// DefaultOn categories are active in a fresh session without being enabled.
	DefaultOn bool `json:"default_on"`
}

// categoryOrder is the canonical, stable presentation order.
var categoryOrder = []CategoryMeta{
	{Name: CategoryTools, Description: "Discover, describe, and enable tool groups.", AlwaysOn: true, DefaultOn: true},
	{Name: CategoryConfirm, Description: "Request and scope user confirmation for risky actions.", AlwaysOn: true, DefaultOn: true},
	{Name: CategoryFiles, Description: "Read and write files in the sandbox.", DefaultOn: true},
	{Name: CategoryWeb, Description: "Search the web, fetch URLs, and summarize fetched pages.", DefaultOn: true},
	{Name: CategorySubagents, Description: "Spawn sub-agents and check their status.", DefaultOn: true},
	{Name: CategorySkills, Description: "Invoke skills (playbooks) for specialized tasks.", DefaultOn: true},
	{Name: CategoryExec, Description: "Run shell commands via bash (network access still requires confirmation).", DefaultOn: true},
	{Name: CategoryMemory, Description: "List, add, update, and delete long-term memory items.", DefaultOn: false},
	{Name: CategoryScheduling, Description: "Run proactive passes and schedule future self-runs.", DefaultOn: false},
	{Name: CategoryMCP, Description: "External Model Context Protocol server tools (per-user, configured by admin).", DefaultOn: false},
	{Name: CategoryAdmin, Description: "Privileged host execution OUTSIDE the sandbox with full permissions. Every call requires a justification and explicit user approval.", DefaultOn: false},
}

var categoryByName = func() map[string]CategoryMeta {
	m := make(map[string]CategoryMeta, len(categoryOrder))
	for _, c := range categoryOrder {
		m[c.Name] = c
	}
	return m
}()

// AllCategories returns the canonical category metadata in presentation order.
func AllCategories() []CategoryMeta {
	out := make([]CategoryMeta, len(categoryOrder))
	copy(out, categoryOrder)
	return out
}

// CategoryByName returns the metadata for a category name.
func CategoryByName(name string) (CategoryMeta, bool) {
	c, ok := categoryByName[name]
	return c, ok
}

// IsValidCategory reports whether name is a known category.
func IsValidCategory(name string) bool {
	_, ok := categoryByName[name]
	return ok
}

// DefaultOnCategories returns the categories active in a fresh session.
func DefaultOnCategories() []string {
	var out []string
	for _, c := range categoryOrder {
		if c.DefaultOn {
			out = append(out, c.Name)
		}
	}
	return out
}

// CategoryNames returns all category names in presentation order.
func CategoryNames() []string {
	out := make([]string, 0, len(categoryOrder))
	for _, c := range categoryOrder {
		out = append(out, c.Name)
	}
	return out
}

// ToolsInCategory returns the names of registered tools in the given category,
// sorted for stable output.
func (r *Registry) ToolsInCategory(category string) []string {
	var out []string
	for _, spec := range r.specs {
		if spec.Category == category {
			out = append(out, spec.Name)
		}
	}
	sort.Strings(out)
	return out
}

// CategoryListing pairs category metadata with its registered tools.
type CategoryListing struct {
	CategoryMeta
	Tools []string `json:"tools"`
}

// Categories returns all categories with their registered tools, in canonical order.
func (r *Registry) Categories() []CategoryListing {
	out := make([]CategoryListing, 0, len(categoryOrder))
	for _, c := range categoryOrder {
		out = append(out, CategoryListing{CategoryMeta: c, Tools: r.ToolsInCategory(c.Name)})
	}
	return out
}
