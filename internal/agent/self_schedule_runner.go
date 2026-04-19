package agent

import (
	"context"
	"errors"
	"strings"
	"time"

	"tether/internal/llm/openrouter"
	"tether/internal/store"
)

// RunSelfSchedule executes a self.schedule job using the full chat agent tool loop,
// restricted to the tool snapshot captured when the schedule was created.
//
// This method is invoked by the proactive scheduler (background context).
func (a *Agent) RunSelfSchedule(ctx context.Context, job store.SelfSchedule, activeTools []string) (string, error) {
	if a == nil {
		return "", errors.New("agent not available")
	}
	if a.db == nil {
		return "", errors.New("db not available")
	}
	if strings.TrimSpace(a.cfg.OpenRouter.APIKey) == "" {
		return "", errors.New("OPENROUTER_API_KEY not configured")
	}
	if job.UserID == 0 || job.ConversationID == 0 {
		return "", errors.New("invalid schedule")
	}

	// Build a session with tool access restricted to the snapshot.
	sess := a.forkSessionFor(job.UserID, job.ConversationID)
	if sess == nil {
		return "", errors.New("session not available")
	}
	snap := map[string]bool{}
	for _, name := range activeTools {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		snap[name] = true
	}
	sess.Active = snap
	sess.IsSubagent = false

	// Reconstruct the conversation context as-of the moment scheduling happened.
	history, err := store.ListRecentMessagesBeforeID(a.db, job.ConversationID, job.AnchorMessageID, 25)
	if err != nil {
		return "", err
	}

	baseItems, err := a.buildContextInputItemsWithSessionAndSystemPrompt(sess, job.UserID, job.ConversationID, history, proactiveSystemPrompt)
	if err != nil {
		return "", err
	}

	createdAt := job.CreatedAt.UTC()
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}

	userText := "Self-scheduled run (background).\n" +
		"Created at: " + createdAt.Format(time.RFC3339) + "\n" +
		"Scheduled for: " + job.RunAt.UTC().Format(time.RFC3339) + "\n" +
		"Now: " + time.Now().UTC().Format(time.RFC3339) + "\n\n" +
		"Task:\n" + strings.TrimSpace(job.Prompt)

	items := append(baseItems, openrouter.ResponseItem{
		Type:    "message",
		Role:    "user",
		Content: []openrouter.ContentPart{{Type: "input_text", Text: userText}},
	})

	text, _, _, err := a.replyWithToolsStream(ctx, sess, job.UserID, job.ConversationID, items, nil, nil)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(text), nil
}
