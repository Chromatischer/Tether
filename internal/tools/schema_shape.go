package tools

import (
	"fmt"
	"sort"
	"strings"
)

// SchemaShape renders a compact, human-readable type shape from a JSON schema-ish value.
// It's meant for tool descriptions (LLM-facing), not strict validation.
func SchemaShape(schema any) string {
	m, ok := schema.(map[string]any)
	if !ok || m == nil {
		return "any"
	}

	// oneOf/anyOf (best-effort)
	if v, ok := m["oneOf"].([]any); ok && len(v) > 0 {
		parts := make([]string, 0, len(v))
		for _, s := range v {
			parts = append(parts, SchemaShape(s))
		}
		return strings.Join(parts, " | ")
	}
	if v, ok := m["anyOf"].([]any); ok && len(v) > 0 {
		parts := make([]string, 0, len(v))
		for _, s := range v {
			parts = append(parts, SchemaShape(s))
		}
		return strings.Join(parts, " | ")
	}

	typ, _ := m["type"].(string)
	switch typ {
	case "object":
		props, _ := m["properties"].(map[string]any)
		reqSet := map[string]bool{}
		if req, ok := m["required"].([]string); ok {
			for _, k := range req {
				reqSet[k] = true
			}
		} else if req, ok := m["required"].([]any); ok {
			for _, k := range req {
				if s, ok := k.(string); ok {
					reqSet[s] = true
				}
			}
		}

		keys := make([]string, 0, len(props))
		for k := range props {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			opt := "?"
			if reqSet[k] {
				opt = ""
			}
			parts = append(parts, fmt.Sprintf("%s%s:%s", k, opt, SchemaShape(props[k])))
		}
		return "{" + strings.Join(parts, ", ") + "}"

	case "array":
		return "[" + SchemaShape(m["items"]) + "]"

	case "string", "integer", "number", "boolean", "null":
		return typ
	}

	// enums (fallback)
	if e, ok := m["enum"].([]any); ok && len(e) > 0 {
		parts := make([]string, 0, len(e))
		for _, v := range e {
			parts = append(parts, fmt.Sprintf("%v", v))
		}
		return strings.Join(parts, " | ")
	}

	if typ != "" {
		return typ
	}
	return "any"
}

// LLMDescription produces the tool description shown to the model.
// Keep it compact; tool descriptions are sent on every LLM call.
func LLMDescription(spec ToolSpec) string {
	d := strings.TrimSpace(spec.Summary)
	if d == "" {
		d = spec.Name
	}

	if spec.InputSchema != nil {
		d += "\nInputs: " + SchemaShape(spec.InputSchema)
	}
	if spec.OutputSchema != nil {
		d += "\nReturns: " + SchemaShape(spec.OutputSchema)
	}
	if strings.TrimSpace(spec.Safety) != "" {
		// first line only
		s := strings.TrimSpace(spec.Safety)
		if i := strings.IndexByte(s, '\n'); i >= 0 {
			s = strings.TrimSpace(s[:i])
		}
		d += "\nSafety: " + s
	}
	return strings.TrimSpace(d)
}
