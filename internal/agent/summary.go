package agent

import (
	"context"
	"strings"
	"time"

	"charm.land/log/v2"

	"tether/internal/llm/openrouter"
	"tether/internal/store"
)

func (a *Agent) maybeUpdateSummary(conversationID int64) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	count, err := store.CountMessages(a.db, conversationID)
	if err != nil {
		return
	}
	if count < 40 {
		return
	}

	_, updatedAt, ok, err := store.GetConversationSummary(a.db, conversationID)
	if err == nil && ok {
		if time.Since(updatedAt) < 30*time.Minute {
			return
		}
	}

	// Summarize last N messages.
	history, err := store.ListRecentMessages(a.db, conversationID, 60)
	if err != nil {
		return
	}

	var b strings.Builder
	b.WriteString("Summarize the following conversation into a compact bullet list capturing: goals, decisions, tasks, user preferences, and important context. Do not include secrets. Keep it under 200 words.\n\n")
	for _, m := range history {
		b.WriteString(m.Role)
		b.WriteString(": ")
		b.WriteString(m.Content)
		b.WriteString("\n")
	}

	msgs := []openrouter.Message{
		{Role: "system", Content: openrouter.Text("You write concise conversation summaries.")},
		{Role: "user", Content: openrouter.Text(b.String())},
	}
	req := openrouter.ChatRequest{
		Model:       a.cfg.OpenRouter.Model,
		Messages:    msgs,
		Temperature: 0.2,
		MaxTokens:   350,
		ToolChoice:  "none",
	}

	resp, err := a.chatCached(ctx, req)
	if err != nil {
		log.Debug("summary update failed", "error", err)
		return
	}
	sum := ""
	if resp.Choices[0].Message.Content != nil {
		sum = strings.TrimSpace(*resp.Choices[0].Message.Content)
	}
	if sum == "" {
		return
	}
	_ = store.SetConversationSummary(a.db, conversationID, sum)
}
