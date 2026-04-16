package agent

import (
	"fmt"
	"os"
	"strings"

	"tether/internal/llm/openrouter"
	"tether/internal/personality"
	"tether/internal/skills"
	"tether/internal/store"
	"tether/internal/userspace"
)

func (a *Agent) buildContextMessages(userID, convID int64, history []store.Message) ([]openrouter.Message, error) {
	sess := a.sessionFor(userID, convID)
	msgs := make([]openrouter.Message, 0, len(history)+10)
	msgs = append(msgs, openrouter.Message{Role: "system", Content: openrouter.Text(systemPrompt)})

	// Per-user personality (self-editable in the sandbox).
	if sess != nil {
		if p := loadPersonalityText(sess.Dirs, personality.AgentChat); strings.TrimSpace(p) != "" {
			rel := "config/agents/chat/PERSONALITY.md"
			if r2, ok := userspace.PersonalityRelPath(personality.AgentChat); ok {
				rel = r2
			}
			msgs = append(msgs, openrouter.Message{Role: "system", Content: openrouter.Text(
				"Agent personality (from " + rel + "). Follow this.\n" +
					"Self-editable: you may update this file as you learn stable user preferences (backups are kept).\n" +
					"Proactive agent personalities live under config/agents/proactive/<agent_id>/PERSONALITY.md (incl. daily_brief, open_loops).\n\n" + p,
			)})
		}
	}

	// Conversation summary (if available)
	if sum, _, ok, err := store.GetConversationSummary(a.db, convID); err == nil && ok {
		sum = strings.TrimSpace(sum)
		if sum != "" {
			msgs = append(msgs, openrouter.Message{Role: "system", Content: openrouter.Text("Conversation summary:\n" + sum)})
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
			msgs = append(msgs, openrouter.Message{Role: "system", Content: openrouter.Text("User facts (top):\n- " + strings.Join(facts, "\n- "))})
		}
		if len(prefs) > 0 {
			msgs = append(msgs, openrouter.Message{Role: "system", Content: openrouter.Text("User preferences (top):\n- " + strings.Join(prefs, "\n- "))})
		}
		if len(tasks) > 0 {
			msgs = append(msgs, openrouter.Message{Role: "system", Content: openrouter.Text("Open tasks:\n- " + strings.Join(tasks, "\n- "))})
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
				msgs = append(msgs, openrouter.Message{Role: "system", Content: openrouter.Text(idx)})
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
			msgs = append(msgs, openrouter.Message{Role: "system", Content: openrouter.Text(picked[i])})
		}
	}

	// OpenRouter prompt caching is provider-side. To maximize cache hits, OpenRouter
	// uses provider sticky routing after cached requests. Sticky routing groups
	// requests into "conversations" by hashing the first system message and the
	// first *non-system* message.
	//
	// We only include a sliding recent-history window, so the first non-system
	// message would drift over time, lowering cache hit rates. Inject a tiny,
	// stable metadata message so the conversation identity stays stable.
	msgs = append(msgs, openrouter.Message{
		Role:    "user",
		Name:    "tether_meta",
		Content: openrouter.Text(fmt.Sprintf("(tether metadata; ignore) conversation_id=%d", convID)),
	})

	for _, m := range history {
		role := m.Role
		switch role {
		case "assistant", "user", "system", "tool":
		case "tool_call":
			// Display-only role stored for the chat UI; not sent to the LLM.
			continue
		default:
			role = "user"
		}
		msgs = append(msgs, openrouter.Message{Role: role, Content: openrouter.Text(m.Content)})
	}
	return msgs, nil
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
