package agent

import (
	"crypto/sha1"
	"encoding/hex"
	"sort"
	"strings"

	"tether/internal/tools"
)

// toolNameMap maps internal Tether tool names (which may contain '.') to
// LLM/provider-safe function names.
//
// Some OpenRouter upstream providers reject function/tool names containing '.'
// (Friendli is one such provider). To remain compatible without renaming tools
// internally, we present a sanitized name to the model and translate tool calls
// back to internal names before execution.
type toolNameMap struct {
	internalToLLM map[string]string
	llmToInternal map[string]string

	// internalNamesDesc is internal tool names sorted by length desc for safe rewriting.
	internalNamesDesc []string
}

func newToolNameMap(reg *tools.Registry) *toolNameMap {
	m := &toolNameMap{internalToLLM: map[string]string{}, llmToInternal: map[string]string{}}
	if reg == nil {
		return m
	}

	// Build a stable mapping for all registered tools.
	names := make([]string, 0, len(reg.List()))
	for _, it := range reg.List() {
		name := strings.TrimSpace(it.Name)
		if name != "" {
			names = append(names, name)
		}
	}
	sort.Strings(names)

	seen := map[string]string{} // llmName -> internalName
	for _, internal := range names {
		llm := sanitizeFunctionName(internal)
		if prev, exists := seen[llm]; exists && prev != internal {
			// Collision: suffix with a short, stable hash.
			sum := sha1.Sum([]byte(internal))
			h := hex.EncodeToString(sum[:])
			llm = llm + "_" + h[:8]
			// Extremely unlikely: ensure uniqueness.
			for {
				if prev2, ok := seen[llm]; !ok || prev2 == internal {
					break
				}
				llm = llm + "_" + h[8:12]
			}
		}
		seen[llm] = internal
		m.internalToLLM[internal] = llm
		m.llmToInternal[llm] = internal
	}

	m.internalNamesDesc = append([]string{}, names...)
	sort.Slice(m.internalNamesDesc, func(i, j int) bool { return len(m.internalNamesDesc[i]) > len(m.internalNamesDesc[j]) })
	return m
}

func (m *toolNameMap) ToLLM(internalName string) string {
	internalName = strings.TrimSpace(internalName)
	if internalName == "" {
		return ""
	}
	if v, ok := m.internalToLLM[internalName]; ok {
		return v
	}
	// Best-effort for names not in registry.
	return sanitizeFunctionName(internalName)
}

func (m *toolNameMap) ToInternal(llmName string) string {
	llmName = strings.TrimSpace(llmName)
	if llmName == "" {
		return ""
	}
	if v, ok := m.llmToInternal[llmName]; ok {
		return v
	}
	// Unknown: treat as already-internal.
	return llmName
}

// RewriteTextToLLM replaces occurrences of internal tool names inside arbitrary
// text with their LLM-visible equivalents.
func (m *toolNameMap) RewriteTextToLLM(text string) string {
	if text == "" || len(m.internalNamesDesc) == 0 {
		return text
	}
	out := text
	for _, internal := range m.internalNamesDesc {
		llm := m.internalToLLM[internal]
		if llm == "" || llm == internal {
			continue
		}
		out = strings.ReplaceAll(out, internal, llm)
	}
	return out
}

func sanitizeFunctionName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(name))
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case c >= 'a' && c <= 'z':
			b.WriteByte(c)
		case c >= 'A' && c <= 'Z':
			b.WriteByte(c)
		case c >= '0' && c <= '9':
			b.WriteByte(c)
		case c == '_' || c == '-':
			b.WriteByte(c)
		default:
			// '.' and any other unsupported char.
			b.WriteByte('_')
		}
	}
	out := strings.Trim(b.String(), "_")
	if out == "" {
		return "tool"
	}
	return out
}

// rewriteAnyStringsToLLM recursively rewrites strings in maps/slices.
// Intended only for tool outputs where tool names are part of the protocol
// (tool.search/tool.describe/tool.enable) or for error messages.
func (m *toolNameMap) rewriteAnyStringsToLLM(v any) any {
	switch x := v.(type) {
	case string:
		return m.RewriteTextToLLM(x)
	case []any:
		out := make([]any, 0, len(x))
		for _, it := range x {
			out = append(out, m.rewriteAnyStringsToLLM(it))
		}
		return out
	case map[string]any:
		out := map[string]any{}
		for k, it := range x {
			out[k] = m.rewriteAnyStringsToLLM(it)
		}
		return out
	default:
		return v
	}
}
