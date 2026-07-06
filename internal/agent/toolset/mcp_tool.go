package toolset

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	mcpinternal "tether/internal/mcp"
	"tether/internal/tools"
)

// MCPTool wraps a single MCP server tool as a Tether tool.
//
// Tool names are exposed as: mcp.<server>.<tool>
//
// Confirmation policy:
// - Untrusted servers: require confirmation for all tool calls.
// - Trusted servers: require confirmation for tools that are not read-only.
//
// Note: MCP tool annotations are hints and may be untrusted.
type MCPTool struct {
	Server  string
	Tool    *mcpsdk.Tool
	Trusted bool
}

func MCPToolName(serverName, toolName string) string {
	serverName = strings.TrimSpace(serverName)
	toolName = strings.TrimSpace(toolName)
	if serverName == "" || toolName == "" {
		return ""
	}
	return "mcp." + serverName + "." + toolName
}

func (t MCPTool) Spec() tools.ToolSpec {
	server := strings.TrimSpace(t.Server)
	name := ""
	desc := ""
	input := any(map[string]any{"type": "object"})
	var output any
	if t.Tool != nil {
		name = MCPToolName(server, t.Tool.Name)
		desc = strings.TrimSpace(t.Tool.Description)
		if desc == "" {
			desc = strings.TrimSpace(t.Tool.Title)
		}
		if t.Tool.InputSchema != nil {
			input = t.Tool.InputSchema
		}
		if t.Tool.OutputSchema != nil {
			output = t.Tool.OutputSchema
		}
	}
	if desc == "" {
		desc = "Call an MCP tool."
	}

	safety := "Calls an external MCP server. "
	if !t.Trusted {
		safety += "Untrusted server: confirmation required for all calls."
	} else {
		// Even trusted servers require confirmation for non-read-only tools.
		safety += "Trusted server: confirmation required for non-read-only tools."
	}

	return tools.ToolSpec{
		Name:         name,
		Category:     tools.CategoryMCP,
		Summary:      desc + " (MCP: " + server + ")",
		Safety:       safety,
		InputSchema:  input,
		OutputSchema: output,
		Tags:         []string{"mcp"},
	}
}

func (t MCPTool) Definition() ToolDef {
	spec := t.Spec()
	return ToolDef{Name: spec.Name, Description: tools.LLMDescription(spec), Parameters: spec.InputSchema}
}

func (t MCPTool) Execute(ctx context.Context, s *Session, rawArgs json.RawMessage) (any, error) {
	if s == nil || s.MCP == nil {
		return nil, fmt.Errorf("mcp system not configured")
	}
	if t.Tool == nil {
		return nil, fmt.Errorf("mcp tool not configured")
	}

	args := map[string]any{}
	if b := strings.TrimSpace(string(rawArgs)); b != "" && b != "null" {
		if err := json.Unmarshal(rawArgs, &args); err != nil {
			return nil, err
		}
	}

	confirmToken := ""
	if v, ok := args["confirm_token"].(string); ok {
		confirmToken = strings.TrimSpace(v)
		delete(args, "confirm_token")
	}

	readOnly := false
	if t.Tool.Annotations != nil {
		readOnly = t.Tool.Annotations.ReadOnlyHint
	}

	requiresConfirm := !t.Trusted || !readOnly
	if requiresConfirm {
		scope := mcpinternal.ConfirmScope(t.Server, t.Tool.Name, args)
		if s.Confirm == nil || !s.Confirm.Consume(s.UserID, confirmToken, scope) {
			return nil, fmt.Errorf("mcp tool call requires confirmation; scope=%q", scope)
		}
	}

	res, err := s.MCP.CallTool(ctx, s.UserID, t.Server, t.Tool.Name, args)
	if err != nil {
		return nil, err
	}
	return sanitizeMCPCallToolResult(res)
}

func sanitizeMCPCallToolResult(res *mcpsdk.CallToolResult) (any, error) {
	if res == nil {
		return map[string]any{"content": []any{}, "isError": false}, nil
	}
	b, err := json.Marshal(res)
	if err != nil {
		return nil, err
	}
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return nil, err
	}
	m, ok := v.(map[string]any)
	if !ok {
		return v, nil
	}

	// Strip or truncate obviously dangerous/huge payloads (base64 images/audio).
	if content, ok := m["content"].([]any); ok {
		out := make([]any, 0, len(content))
		for _, it := range content {
			obj, ok := it.(map[string]any)
			if !ok {
				out = append(out, it)
				continue
			}
			typ, _ := obj["type"].(string)
			switch typ {
			case "image", "audio":
				mime, _ := obj["mimeType"].(string)
				sz := 0
				if data, ok := obj["data"].(string); ok {
					sz = len(data)
				}
				out = append(out, map[string]any{
					"type": "text",
					"text": fmt.Sprintf("[%s omitted: mimeType=%s, dataLen=%d]", typ, mime, sz),
				})
			default:
				if typ == "text" {
					if text, ok := obj["text"].(string); ok && len(text) > 12000 {
						obj["text"] = text[:12000] + "… (truncated)"
					}
				}
				out = append(out, obj)
			}
		}
		m["content"] = out
	}

	// structuredContent can be huge; keep it if it is reasonably small.
	if sc, ok := m["structuredContent"]; ok {
		if bb, err := json.Marshal(sc); err == nil && len(bb) > 20000 {
			delete(m, "structuredContent")
			// Add a note to content.
			if content, ok := m["content"].([]any); ok {
				m["content"] = append(content, map[string]any{"type": "text", "text": "[structuredContent omitted: too large]"})
			}
		}
	}

	return m, nil
}
