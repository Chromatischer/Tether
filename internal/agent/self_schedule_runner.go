package agent

import (
	"context"
	"errors"
	"strings"

	"tether/internal/llm/openrouter"
	"tether/internal/store"
)

// RunSelfSchedule runs a fired self-schedule as a "wakeup": it continues the
// conversation from exactly where it left off. It uses the current conversation
// history, the normal chat system prompt, and the live session/tool set — nothing
// is special-cased. The scheduled prompt is injected as the next turn, exactly as
// if it had been sent into the live chat.
//
// This method is invoked by the proactive scheduler (background context).
func (a *Agent) RunSelfSchedule(ctx context.Context, job store.SelfSchedule) (string, error) {
	if a == nil {
		return "", errors.New("agent not available")
	}
	if a.db == nil {
		return "", errors.New("db not available")
	}
	if strings.TrimSpace(a.cfg.LLMAPIKey()) == "" {
		return "", errors.New(a.cfg.LLMAPIKeyEnvName() + " not configured")
	}
	if job.UserID == 0 || job.ConversationID == 0 {
		return "", errors.New("invalid schedule")
	}
	prompt := strings.TrimSpace(job.Prompt)
	if prompt == "" {
		return "", errors.New("empty schedule prompt")
	}

	// Live session — same tools and state as the ongoing chat. We deliberately do
	// NOT restrict to a snapshot: a wakeup continues the conversation as it is now.
	sess := a.forkSessionFor(job.UserID, job.ConversationID)
	if sess == nil {
		return "", errors.New("session not available")
	}
	defer a.mergeSessionFor(job.ConversationID, sess)
	sess.IsSubagent = false

	// Build context exactly like a normal chat turn: current history + the normal
	// chat system prompt (no proactive prompt, no anchor snapshot).
	items, err := a.buildContextInputItemsWithSession(ctx, sess, job.UserID, job.ConversationID, nil)
	if err != nil {
		return "", err
	}

	// The scheduled prompt is the wakeup turn that continues the conversation.
	items = append(items, openrouter.ResponseItem{
		Type:    "message",
		Role:    "user",
		Content: []openrouter.ContentPart{{Type: "input_text", Text: prompt}},
	})

	text, _, _, _, err := a.replyWithToolsStream(ctx, sess, job.UserID, job.ConversationID, items, nil, nil)
	if err != nil {
		return "", err
	}

	// Keep the rolling summary fresh, like a normal turn does.
	go a.maybeUpdateSummary(job.ConversationID)
	return strings.TrimSpace(text), nil
}
