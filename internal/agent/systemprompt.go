package agent

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"tether/internal/store"
	"tether/internal/systemprompt"
	"tether/internal/userspace"
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

func (a *Agent) renderSystemPromptForUser(userID, convID int64, templateName, mode string) string {
	d := userspace.ForUser(a.cfg.Paths.DataDir, userID)
	_ = userspace.Ensure(d)
	_ = userspace.EnsurePromptTemplateFile(d, templateName)

	source := loadSystemPromptTemplate(d, templateName)
	if strings.TrimSpace(source) == "" {
		return a.renderDefaultSystemPrompt(templateName, systemprompt.TemplateData{
			UserID:         userID,
			ConversationID: convID,
			SessionID:      sessionIDFor(convID),
			Mode:           mode,
		})
	}

	return systemprompt.Render(source, systemprompt.TemplateData{
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

func loadSystemPromptTemplate(d userspace.Dirs, templateName string) string {
	p, ok := userspace.PromptTemplateAbsPath(d, templateName)
	if !ok {
		return ""
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	out := strings.TrimSpace(string(b))
	const max = 32 * 1024
	if len(out) > max {
		out = out[:max] + "\n... (truncated)"
	}
	return out
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

func promptSourceRef(templateName string) string {
	rel, ok := userspace.PromptTemplateRelPath(templateName)
	if !ok {
		return fmt.Sprintf("template %q", templateName)
	}
	return rel
}
