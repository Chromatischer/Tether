package agent

import (
	"context"
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

func (a *Agent) buildContextInputItems(ctx context.Context, userID, convID int64, history []store.Message) ([]openrouter.ResponseItem, error) {
	return a.buildContextInputItemsWithSession(ctx, a.sessionFor(userID, convID), userID, convID, history)
}

func (a *Agent) buildContextInputItemsWithSession(ctx context.Context, sess *toolset.Session, userID, convID int64, history []store.Message) ([]openrouter.ResponseItem, error) {
	return a.buildContextInputItemsWithSessionAndSystemPrompt(ctx, sess, userID, convID, history, a.chatSystemPromptText(userID, convID))
}

func (a *Agent) buildContextInputItemsWithSessionAndSystemPrompt(ctx context.Context, sess *toolset.Session, userID, convID int64, history []store.Message, sysPrompt string) ([]openrouter.ResponseItem, error) {
	if convID != 0 && len(history) == 0 && a.db != nil {
		var err error
		history, err = a.prepareConversationHistoryForContext(ctx, convID)
		if err != nil {
			return nil, err
		}
	}
	items := make([]openrouter.ResponseItem, 0, len(history)+10)

	nm := newToolNameMap(nil)
	if sess != nil {
		nm = newToolNameMap(sess.Registry)
	}

	// Some providers reject tool/function names containing '.'; rewrite any
	// internal tool names mentioned in prompts to their LLM-visible equivalents.
	sysText := sysPrompt
	if nm != nil {
		sysText = nm.RewriteTextToLLM(sysText)
	}
	items = append(items, openrouter.ResponseItem{Type: "message", Role: "system", Content: []openrouter.ContentPart{{Type: "input_text", Text: sysText}}})

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
	if st, ok, err := store.GetConversationSummaryState(a.db, convID); err == nil && ok {
		if summaryRef := formatConversationSummaryReference(st.Summary); summaryRef != "" {
			items = append(items, openrouter.ResponseItem{Type: "message", Role: "user", Content: []openrouter.ContentPart{{Type: "input_text", Text: summaryRef}}})
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
		if !sess.IsSubagent {
			mgr := skills.NewManager()
			if list, err := mgr.List(sess.Dirs); err == nil {
				idx := strings.TrimSpace(mgr.BuildIndexMessage(list))
				if idx != "" {
					if nm != nil {
						idx = nm.RewriteTextToLLM(idx)
					}
					items = append(items, openrouter.ResponseItem{Type: "message", Role: "system", Content: []openrouter.ContentPart{{Type: "input_text", Text: idx}}})
				}
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
		case "tool_call", "assistant_reasoning":
			// Display-only roles stored for the chat UI; do not send them back to the model.
			continue
		default:
			// user + any unknown role
			items = append(items, openrouter.ResponseItem{Type: "message", Role: "user", Content: []openrouter.ContentPart{{Type: "input_text", Text: m.Content}}})
		}
	}
	return items, nil
}

func (a *Agent) prepareConversationHistoryForContext(ctx context.Context, conversationID int64) ([]store.Message, error) {
	if a == nil || a.db == nil || conversationID == 0 {
		return nil, nil
	}
	modelLimit := a.modelInfo(a.cfg.OpenRouter.Model).ContextLength
	if modelLimit <= 0 {
		modelLimit = 128000
	}
	compactThreshold := min(int(float64(modelLimit)*0.80), 200_000)
	if compactThreshold <= 0 {
		compactThreshold = 100_000
	}
	rawTailBudget := int(float64(modelLimit) * 0.22)
	if rawTailBudget <= 0 {
		rawTailBudget = 28000
	}

	if err := a.compactConversationIfNeeded(ctx, conversationID, compactThreshold, rawTailBudget); err != nil {
		return nil, err
	}

	st, ok, err := store.GetConversationSummaryState(a.db, conversationID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return store.ListMessagesAfterID(a.db, conversationID, 0)
	}
	return store.ListMessagesAfterID(a.db, conversationID, st.SummarizedThroughMessageID)
}

func (a *Agent) compactConversationIfNeeded(ctx context.Context, conversationID int64, compactThreshold, rawTailBudget int) error {
	st, ok, err := store.GetConversationSummaryState(a.db, conversationID)
	if err != nil {
		return err
	}
	afterID := int64(0)
	if ok {
		afterID = st.SummarizedThroughMessageID
	}
	history, err := store.ListMessagesAfterID(a.db, conversationID, afterID)
	if err != nil {
		return err
	}
	if estimateMessagesTokens(history) <= compactThreshold {
		return nil
	}
	if len(history) < 8 {
		return nil
	}

	tailStart := findTailStartByBudget(history, rawTailBudget)
	if tailStart <= 0 || tailStart >= len(history) {
		return nil
	}
	compactSlice := history[:tailStart]
	throughID := compactSlice[len(compactSlice)-1].ID
	var prior string
	if ok {
		prior = strings.TrimSpace(st.Summary)
	}
	if prior != "" {
		prefix := store.Message{ID: afterID, Role: "system", Content: "Previously compacted conversation summary:\n" + prior}
		compactSlice = append([]store.Message{prefix}, compactSlice...)
	}
	sum, err := a.compactConversationSlice(ctx, compactSlice, 300)
	if err != nil {
		return err
	}
	if strings.TrimSpace(sum) == "" {
		return nil
	}
	return store.SetConversationSummaryState(a.db, conversationID, sum, throughID)
}

func estimateMessagesTokens(history []store.Message) int {
	total := 0
	for _, m := range history {
		total += estimateTextTokens(m.Role)
		total += estimateTextTokens(m.Content)
	}
	return total
}

func estimateTextTokens(s string) int {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	return (len(s)+3)/4 + 8
}

func findTailStartByBudget(history []store.Message, budget int) int {
	if budget <= 0 {
		return len(history)
	}
	total := 0
	for i := len(history) - 1; i >= 0; i-- {
		total += estimateTextTokens(history[i].Role)
		total += estimateTextTokens(history[i].Content)
		if total > budget {
			if i+1 < len(history) {
				return i + 1
			}
			return i
		}
	}
	return 0
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
