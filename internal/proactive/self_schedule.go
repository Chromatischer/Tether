package proactive

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"charm.land/log/v2"

	"tether/internal/store"
)

const (
	selfScheduleNotificationKind = "self_schedule"
	selfScheduleMaxPerTick       = 10
)

func (e *Engine) tickSelfSchedules(ctx context.Context, userID int64, now time.Time) {
	_ = ctx
	if e == nil || e.db == nil {
		return
	}
	if userID == 0 {
		return
	}

	due, err := store.ListDueSelfSchedules(e.db, userID, now, selfScheduleMaxPerTick)
	if err != nil || len(due) == 0 {
		return
	}

	for _, it := range due {
		claimed, err := store.TryClaimSelfSchedule(e.db, it.ID)
		if err != nil || !claimed {
			continue
		}
		tools := parseActiveToolsJSON(it.ActiveToolsJSON)
		go e.runSelfSchedule(context.Background(), userID, it, tools)
	}
}

func parseActiveToolsJSON(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	var out []string
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return nil
	}
	// Best-effort cleanup.
	clean := make([]string, 0, len(out))
	seen := map[string]bool{}
	for _, t := range out {
		t = strings.TrimSpace(t)
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		clean = append(clean, t)
	}
	return clean
}

func (e *Engine) runSelfSchedule(ctx context.Context, userID int64, it store.SelfSchedule, activeTools []string) {
	// Fixed deadline; proactive scheduler must not block indefinitely.
	ctx2, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	payload, _ := json.Marshal(map[string]any{
		"schedule_id":     it.ID,
		"conversation_id": it.ConversationID,
		"tools_n":         len(activeTools),
	})
	_ = store.AddAuditEvent(e.db, &userID, "self_schedule_trigger", string(payload))

	var text string
	var err error

	// Preferred: run full agent loop with tool calling.
	if e.selfRunner != nil {
		text, err = e.selfRunner.RunSelfSchedule(ctx2, it, activeTools)
	} else if e.llm != nil {
		// Fallback: text-only proactive prompt (no tool loop).
		prompt := e.buildSelfSchedulePrompt(userID, it)
		text, err = e.llm.RunProactivePrompt(ctx2, prompt)
	} else {
		return
	}

	if err != nil {
		errMsg := "(self-schedule error: " + err.Error() + ")"
		_ = store.MarkSelfScheduleError(e.db, it.ID, err.Error())
		_ = store.AddNotificationForConversation(e.db, userID, it.ConversationID, selfScheduleNotificationKind, errMsg)
		return
	}

	out := strings.TrimSpace(text)
	if out == "" {
		out = "(empty)"
	}
	if err := store.AddNotificationForConversation(e.db, userID, it.ConversationID, selfScheduleNotificationKind, out); err != nil {
		log.Warn("self-schedule: add notification failed", "error", err)
	}
	_ = store.MarkSelfScheduleDone(e.db, it.ID)
}

func (e *Engine) buildSelfSchedulePrompt(userID int64, it store.SelfSchedule) string {
	// Conversation summary (optional)
	sum := ""
	if it.ConversationID > 0 {
		if s2, _, ok, _ := store.GetConversationSummary(e.db, it.ConversationID); ok {
			sum = strings.TrimSpace(s2)
		}
	}

	// Transcript slice anchored to the message id at scheduling time.
	msgs, _ := store.ListRecentMessagesBeforeID(e.db, it.ConversationID, it.AnchorMessageID, 25)

	// Tasks (lightweight extra context)
	tasks, _ := store.ListMemoryItems(e.db, userID, "task", 25)

	var b strings.Builder
	b.WriteString("This is a self-scheduled proactive run created earlier from within an ongoing chat thread.\n")
	b.WriteString("Now: ")
	b.WriteString(time.Now().UTC().Format(time.RFC3339))
	b.WriteString("\nScheduled for: ")
	b.WriteString(it.RunAt.UTC().Format(time.RFC3339))
	b.WriteString("\n\n")
	b.WriteString("Scheduled prompt/instructions:\n")
	b.WriteString(strings.TrimSpace(it.Prompt))
	b.WriteString("\n\n")
	if sum != "" {
		b.WriteString("Conversation summary (at/near scheduling time):\n")
		b.WriteString(sum)
		b.WriteString("\n\n")
	}

	if len(msgs) > 0 {
		b.WriteString("Conversation context (messages up to the scheduling anchor):\n")
		for _, m := range msgs {
			role := strings.TrimSpace(m.Role)
			if role == "" {
				role = "user"
			}
			content := strings.TrimSpace(m.Content)
			if content == "" {
				continue
			}
			// Keep it compact.
			if len(content) > 900 {
				content = content[:900] + "…"
			}
			b.WriteString(strings.ToUpper(role[:1]) + role[1:] + ": ")
			b.WriteString(content)
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	if len(tasks) > 0 {
		b.WriteString("Open tasks:\n")
		for i, t := range tasks {
			if i >= 10 {
				break
			}
			b.WriteString("- ")
			b.WriteString(strings.TrimSpace(t.Content))
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	b.WriteString("Write the proactive assistant message to the user. Keep it under 140 words.")
	return b.String()
}
