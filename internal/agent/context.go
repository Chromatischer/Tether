package agent

import (
	"fmt"
	"os"
	"strings"

	"tether/internal/agent/toolset"
	"tether/internal/llm/openrouter"
	"tether/internal/personality"
	"tether/internal/skills"
	"tether/internal/store"
	"tether/internal/userspace"
)

func (a *Agent) buildContextInputItems(userID, convID int64, history []store.Message) ([]openrouter.ResponseItem, error) {
	return a.buildContextInputItemsWithSession(a.sessionFor(userID, convID), userID, convID, history)
}

func (a *Agent) buildContextInputItemsWithSession(sess *toolset.Session, userID, convID int64, history []store.Message) ([]openrouter.ResponseItem, error) {
	items := make([]openrouter.ResponseItem, 0, len(history)+10)

	items = append(items, openrouter.ResponseItem{Type: "message", Role: "system", Content: []openrouter.ContentPart{{Type: "input_text", Text: systemPrompt}}})

	// Per-user personality (self-editable in the sandbox).
	if sess != nil {
		if p := loadPersonalityText(sess.Dirs, personality.AgentChat); strings.TrimSpace(p) != "" {
			rel := "config/agents/chat/PERSONALITY.md"
			if r2, ok := userspace.PersonalityRelPath(personality.AgentChat); ok {
				rel = r2
			}
			items = append(items, openrouter.ResponseItem{Type: "message", Role: "system", Content: []openrouter.ContentPart{{
				Type: "input_text",
				Text: "Agent personality (from " + rel + "). Follow this.\n" +
					"Self-editable: you may update this file as you learn stable user preferences (backups are kept).\n" +
					"Proactive agent personalities live under config/agents/proactive/<agent_id>/PERSONALITY.md (incl. daily_brief, open_loops).\n\n" + p,
			}}})
		}
	}

	// Conversation summary (if available)
	if sum, _, ok, err := store.GetConversationSummary(a.db, convID); err == nil && ok {
		sum = strings.TrimSpace(sum)
		if sum != "" {
			items = append(items, openrouter.ResponseItem{Type: "message", Role: "system", Content: []openrouter.ContentPart{{Type: "input_text", Text: "Conversation summary:\n" + sum}}})
		}
	}

	// Long-term memory (facts/prefs/tasks)
	mem, err := store.ListMemoryItems(a.db, userID, "", 200)
	if err == nil {
		facts := make([]string, 0, 20)
		prefs := make([]string, 0, 20)
		tasks := make([]string, 0, 20)
		usedIDs := make([]int64, 0, 40)
		for _, it := range mem {
			switch it.Kind {
			case "fact":
				if len(facts) < 15 {
					facts = append(facts, it.Content)
					usedIDs = append(usedIDs, it.ID)
				}
			case "pref":
				if len(prefs) < 15 {
					prefs = append(prefs, it.Content)
					usedIDs = append(usedIDs, it.ID)
				}
			case "task":
				if len(tasks) < 10 {
					tasks = append(tasks, it.Content)
					usedIDs = append(usedIDs, it.ID)
				}
			}
		}
		if len(facts) > 0 {
			items = append(items, openrouter.ResponseItem{Type: "message", Role: "system", Content: []openrouter.ContentPart{{Type: "input_text", Text: "User facts (top):\n- " + strings.Join(facts, "\n- ")}}})
		}
		if len(prefs) > 0 {
			items = append(items, openrouter.ResponseItem{Type: "message", Role: "system", Content: []openrouter.ContentPart{{Type: "input_text", Text: "User preferences (top):\n- " + strings.Join(prefs, "\n- ")}}})
		}
		if len(tasks) > 0 {
			items = append(items, openrouter.ResponseItem{Type: "message", Role: "system", Content: []openrouter.ContentPart{{Type: "input_text", Text: "Open tasks:\n- " + strings.Join(tasks, "\n- ")}}})
		}
		if len(usedIDs) > 0 {
			_ = store.TouchMemoryItems(a.db, usedIDs)
		}
	}

	// Skills: show the model what skills exist, and re-attach any invoked skill bodies.
	if sess != nil {
		mgr := skills.NewManager()
		if list, err := mgr.List(sess.Dirs); err == nil {
			idx := strings.TrimSpace(mgr.BuildIndexMessage(list))
			if idx != "" {
				items = append(items, openrouter.ResponseItem{Type: "message", Role: "system", Content: []openrouter.ContentPart{{Type: "input_text", Text: idx}}})
			}
		}

		// Re-attach invoked skill content (most recent first, under a combined budget).
		const totalBudget = 25_000
		picked := make([]string, 0, len(sess.InvokedSkills))
		total := 0
		for i := len(sess.InvokedSkills) - 1; i >= 0; i-- {
			it := sess.InvokedSkills[i]
			content := strings.TrimSpace(it.Content)
			if content == "" {
				continue
			}
			msg := "Skill /" + it.Name + " (invoked):\n" + content
			if total+len(msg) > totalBudget {
				break
			}
			total += len(msg)
			picked = append(picked, msg)
		}
		// Preserve chronological order (older → newer).
		for i := len(picked) - 1; i >= 0; i-- {
			items = append(items, openrouter.ResponseItem{Type: "message", Role: "system", Content: []openrouter.ContentPart{{Type: "input_text", Text: picked[i]}}})
		}
	}

	// Stable metadata marker to keep provider-side caching/sticky routing stable.
	items = append(items, openrouter.ResponseItem{Type: "message", Role: "user", Content: []openrouter.ContentPart{{Type: "input_text", Text: fmt.Sprintf("(tether metadata; ignore) conversation_id=%d", convID)}}})

	for _, m := range history {
		role := m.Role
		switch role {
		case "assistant":
			if strings.TrimSpace(m.Content) == "" {
				continue
			}
			items = append(items, openrouter.ResponseItem{
				Type:   "message",
				Role:   "assistant",
				ID:     fmt.Sprintf("msg_db_%d", m.ID),
				Status: "completed",
				Content: []openrouter.ContentPart{{
					Type: "output_text",
					Text: m.Content,
				}},
			})
		case "system":
			items = append(items, openrouter.ResponseItem{Type: "message", Role: "system", Content: []openrouter.ContentPart{{Type: "input_text", Text: m.Content}}})
		case "tool_call":
			// Display-only role stored for the chat UI; do not send it back to the model.
			continue
		default:
			// user + any unknown role
			items = append(items, openrouter.ResponseItem{Type: "message", Role: "user", Content: []openrouter.ContentPart{{Type: "input_text", Text: m.Content}}})
		}
	}
	return items, nil
}

func loadPersonalityText(d userspace.Dirs, agentKey string) string {
	// Ensure the file exists (best-effort).
	_ = userspace.EnsurePersonalityFile(d, agentKey)

	p, ok := userspace.PersonalityAbsPath(d, agentKey)
	if !ok {
		return ""
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	out := strings.TrimSpace(string(b))
	const max = 8 * 1024
	if len(out) > max {
		out = out[:max] + "\n... (truncated)"
	}
	return out
}
