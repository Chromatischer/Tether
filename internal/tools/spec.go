package tools

// ToolSpec is the canonical documentation for a tool.
// It is used for:
//   - human-facing docs (/tools describe, docs/tools.md)
//   - model-facing tool definitions (ToolDef.Description + parameters JSON schema)
//
// InputSchema and OutputSchema are JSON-Schema-like objects represented as Go maps/slices
// that can be JSON-marshaled.
//
// Note: OpenAI/OpenRouter tool calling only supports *input* schemas. Output schemas are
// informational/documentation only.

type ToolExample struct {
	Title  string `json:"title,omitempty"`
	Args   any    `json:"args"`
	Result any    `json:"result,omitempty"`
	Notes  string `json:"notes,omitempty"`
}

type ToolSpec struct {
	Name         string        `json:"name"`
	Summary      string        `json:"summary"`
	WhenToUse    string        `json:"when_to_use,omitempty"`
	Safety       string        `json:"safety,omitempty"`
	InputSchema  any           `json:"input_schema"`
	OutputSchema any           `json:"output_schema,omitempty"`
	Examples     []ToolExample `json:"examples,omitempty"`
	Tags         []string      `json:"tags,omitempty"`
}

// ToolInfo is a short, list-friendly view of a tool.
// Kept intentionally compact for tool.search outputs.
//
// Note: JSON tags are important; this is returned to the LLM.
// (We intentionally use lowercase keys to match typical tool-call payloads.)
type ToolInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

func (s ToolSpec) Info() ToolInfo {
	return ToolInfo{Name: s.Name, Description: s.Summary}
}
