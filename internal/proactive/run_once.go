package proactive

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"tether/internal/store"
)

// RunOnce runs a proactive evaluation for a single user immediately.
//
// v0.1 behavior: generate a daily brief right now and store it as a notification.
func RunOnce(ctx context.Context, db *sql.DB, llm LLM, userID int64, convID int64) (string, error) {
	if llm == nil {
		return "", context.Canceled
	}

	// Summary + tasks.
	sum := ""
	if s2, _, ok, _ := store.GetConversationSummary(db, convID); ok {
		sum = strings.TrimSpace(s2)
	}
	tasks, _ := store.ListMemoryItems(db, userID, "task", 50)
	var tb strings.Builder
	for _, t := range tasks {
		tb.WriteString("- ")
		tb.WriteString(t.Content)
		tb.WriteString("\n")
	}

	prompt := "Write a short daily brief. Include:\n- top priorities\n- open tasks\n- suggested next actions\nKeep it under 120 words.\n\n"
	if sum != "" {
		prompt += "Conversation summary:\n" + sum + "\n\n"
	}
	if tb.Len() > 0 {
		prompt += "Tasks:\n" + tb.String() + "\n"
	}

	ctx2, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	text, err := llm.RunProactivePrompt(ctx2, prompt)
	if err != nil {
		return "", err
	}
	text = strings.TrimSpace(text)
	if text == "" {
		text = "(empty brief)"
	}
	// Mark as delivered to avoid reinjecting into the chat on the next login.
	_ = store.AddNotificationDelivered(db, userID, "manual_proactive", text)
	return text, nil
}
