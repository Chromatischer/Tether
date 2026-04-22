package agent

import (
	"context"
	"encoding/json"
	"sort"
	"strings"

	"tether/internal/agent/toolset"
	"tether/internal/llm/openrouter"
	"tether/internal/subagents"
	"tether/internal/userspace"
)

type agentSubagentRunner struct {
	ag *Agent
}

func (r agentSubagentRunner) Run(ctx context.Context, userID int64, req subagents.RunRequest, emit func(subagents.ProgressEvent)) (string, error) {
	sess, err := r.ag.newSubagentSession(userID, req)
	if err != nil {
		return "", err
	}
	items, err := r.ag.buildContextInputItemsWithSessionAndSystemPrompt(ctx, sess, userID, 0, nil, r.ag.chatSystemPromptText(userID, 0))
	if err != nil {
		return "", err
	}
	items = append(items, openrouter.ResponseItem{
		Type:    "message",
		Role:    "system",
		Content: []openrouter.ContentPart{{Type: "input_text", Text: subagentSystemAddendum(sess)}},
	})
	items = append(items, openrouter.ResponseItem{
		Type:    "message",
		Role:    "user",
		Content: []openrouter.ContentPart{{Type: "input_text", Text: req.Prompt}},
	})

	text, _, _, err := r.ag.replyWithToolsStream(ctx, sess, userID, 0, items, nil, progressEmitter(emit))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(text), nil
}

// Subagents returns the shared subagent manager.
func (a *Agent) Subagents() *subagents.Manager {
	return a.subMgr
}

func (a *Agent) newSubagentSession(userID int64, req subagents.RunRequest) (*toolset.Session, error) {
	s := toolset.NewSession(a.registry)
	s.UserID = userID
	s.ConversationID = 0
	s.IsSubagent = true
	s.Dirs = userspace.ForUser(a.cfg.Paths.DataDir, userID)
	_ = userspace.Ensure(s.Dirs)
	s.DB = a.db
	s.Subagents = a.subStore
	s.Confirm = auditedConfirmer{mgr: a.confirm, db: a.db}
	s.MCP = a.mcp
	s.LLM = a
	if a.secrets != nil {
		s.Secrets = auditedSecrets{store: a.secrets, db: a.db}
	}

	allowed := allowedSubagentTools(req.AllowedTools)
	s.Allowed = allowed
	s.Active = make(map[string]bool, len(allowed))
	for name := range allowed {
		s.Active[name] = true
	}
	if req.SkillName != "" {
		if err := preloadSubagentSkill(context.Background(), s, req.SkillName, req.SkillArgs); err != nil {
			return nil, err
		}
	}
	return s, nil
}

func subagentSystemAddendum(sess *toolset.Session) string {
	tools := make([]string, 0, len(sess.Active))
	for name := range sess.Active {
		tools = append(tools, name)
	}
	sort.Strings(tools)
	if len(tools) == 0 {
		return "You are running as a subagent. You cannot spawn subagents or invoke additional skills. No tools are available in this run."
	}
	return "You are running as a subagent. You cannot spawn subagents or invoke additional skills. Use only these tools for this run: " + strings.Join(tools, ", ") + "."
}

func allowedSubagentTools(requested []string) map[string]bool {
	allowed := map[string]bool{}
	for _, name := range requested {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		switch name {
		case "subagent.spawn", "subagent.status", "skill.invoke":
			continue
		default:
			allowed[name] = true
		}
	}
	return allowed
}

func preloadSubagentSkill(ctx context.Context, s *toolset.Session, name string, arguments string) error {
	tool := toolset.SkillInvoke{}
	raw := []byte(`{"name":` + jsonString(name) + `,"arguments":` + jsonString(arguments) + `}`)
	_, err := tool.Execute(ctx, s, raw)
	return err
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func progressEmitter(emit func(subagents.ProgressEvent)) func(StreamEvent) {
	if emit == nil {
		return nil
	}
	return func(ev StreamEvent) {
		switch ev.Type {
		case "assistant_delta":
			emit(subagents.ProgressEvent{Type: "assistant", Text: ev.Text})
		case "tool_call":
			emit(subagents.ProgressEvent{Type: "tool_call", Text: formatProgressTool("calling", ev.Tool)})
		case "tool_result":
			emit(subagents.ProgressEvent{Type: "tool_result", Text: formatProgressTool("finished", ev.Tool)})
		case "done":
			emit(subagents.ProgressEvent{Type: "done", Text: ev.Text})
		case "error":
			emit(subagents.ProgressEvent{Type: "error", Text: ev.Err})
		}
	}
}

func formatProgressTool(verb string, tool ToolCallInfo) string {
	name := strings.TrimSpace(tool.Name)
	args := strings.TrimSpace(tool.Args)
	if name == "" {
		return ""
	}
	if args == "" {
		return "Tool " + verb + ": " + name
	}
	return "Tool " + verb + ": " + name + " " + args
}
