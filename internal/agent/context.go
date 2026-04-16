package agent

import (
	"strings"

	"tether/internal/llm/openrouter"
	"tether/internal/store"
)

func (a *Agent) buildContextMessages(userID, convID int64, history []store.Message) ([]openrouter.Message, error) {
	msgs := make([]openrouter.Message, 0, len(history)+4)
	msgs = append(msgs, openrouter.Message{Role: "system", Content: openrouter.Text(systemPrompt)})

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

	for _, m := range history {
		role := m.Role
		switch role {
		case "assistant", "user", "system", "tool":
		default:
			role = "user"
		}
		msgs = append(msgs, openrouter.Message{Role: role, Content: openrouter.Text(m.Content)})
	}
	return msgs, nil
}
