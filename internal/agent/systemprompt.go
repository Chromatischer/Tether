package agent

import (
	"strconv"
	"strings"

	"tether/internal/store"
	"tether/internal/systemprompt"
)

func (a *Agent) chatSystemPromptText(userID, convID int64) string {
	return a.renderSystemPromptForUser(userID, convID, systemprompt.TemplateChat, "chat")
}

func (a *Agent) proactiveSystemPromptText(userID, convID int64) string {
	return a.renderSystemPromptForUser(userID, convID, systemprompt.TemplateProactive, "proactive")
}

func (a *Agent) defaultChatSystemPromptText() string {
	return a.renderDefaultSystemPrompt(systemprompt.TemplateChat, systemprompt.TemplateData{Mode: "chat"})
}

func (a *Agent) defaultProactiveSystemPromptText() string {
	return a.renderDefaultSystemPrompt(systemprompt.TemplateProactive, systemprompt.TemplateData{Mode: "proactive"})
}

// renderSystemPromptForUser renders the global, embedded system prompt template.
// The system prompt is the same for every user — unlike PERSONALITY.md, which is
// per-user and editable on disk. Only the rendered template data (username,
// mode, ids) varies between users.
func (a *Agent) renderSystemPromptForUser(userID, convID int64, templateName, mode string) string {
	return a.renderDefaultSystemPrompt(templateName, systemprompt.TemplateData{
		Username:       a.usernameFor(userID),
		UserID:         userID,
		ConversationID: convID,
		SessionID:      sessionIDFor(convID),
		Mode:           mode,
	})
}

func (a *Agent) renderDefaultSystemPrompt(templateName string, data systemprompt.TemplateData) string {
	source := systemprompt.DefaultMarkdown(templateName)
	if strings.TrimSpace(source) == "" {
		return ""
	}
	return systemprompt.Render(source, data)
}

func (a *Agent) usernameFor(userID int64) string {
	if a == nil || a.db == nil || userID <= 0 {
		return ""
	}
	u, ok, err := store.GetUserByID(a.db, userID)
	if err != nil || !ok || u == nil {
		return ""
	}
	return strings.TrimSpace(u.Username)
}

func sessionIDFor(convID int64) string {
	if convID <= 0 {
		return ""
	}
	return strconv.FormatInt(convID, 10)
}
