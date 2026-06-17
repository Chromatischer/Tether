package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"tether/internal/agent/toolset"
)

// codeToolInvoker implements toolset.ToolInvoker for the `code` tool. It maps a
// tool name (accepting either the internal canonical name or the LLM-facing
// sanitized name), enforces the active-tool gate, refuses to invoke `code`
// recursively, and translates confirmation-required errors into a clear message
// since the confirmation flow cannot pause a running script.
type codeToolInvoker struct{ a *Agent }

func (ci codeToolInvoker) Invoke(ctx context.Context, s *toolset.Session, name string, rawArgs json.RawMessage) (any, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("tool name required")
	}

	internal := name
	if ci.a.toolImpl[internal] == nil {
		// The model may pass the LLM-facing (sanitized) name; map it back.
		internal = newToolNameMap(ci.a.registry).ToInternal(name)
	}
	if internal == "code" {
		return nil, fmt.Errorf("the 'code' tool cannot be called from within code mode")
	}

	impl := ci.a.toolImpl[internal]
	if impl == nil {
		return nil, fmt.Errorf("unknown tool: %s. Use tools() to see what is enabled in this session", name)
	}
	if !s.IsActive(internal) {
		// The name resolved fine; it just isn't enabled. Say so explicitly so the
		// caller doesn't mistake this for a name-form problem.
		return nil, fmt.Errorf("tool %q resolved but is not enabled in this session. Enable it (tool.enable) before calling it from code; tools() lists what is currently callable", internal)
	}

	result, err := impl.Execute(ctx, s, rawArgs)
	if err != nil {
		if scope := confirmationScopeFromError(err.Error()); scope != "" {
			return nil, fmt.Errorf("tool %q requires user confirmation and cannot be called from code mode; call it directly instead", internal)
		}
		return nil, err
	}
	return result, nil
}
