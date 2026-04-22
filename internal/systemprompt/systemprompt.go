package systemprompt

import (
	"embed"
	"strings"
	"text/template"
)

const (
	TemplateChat      = "chat_system"
	TemplateProactive = "proactive_system"
)

type TemplateData struct {
	Username       string
	UserID         int64
	ConversationID int64
	SessionID      string
	Mode           string
}

//go:embed templates/*.md
var templatesFS embed.FS

func DefaultMarkdown(name string) string {
	path := templatePath(name)
	if path == "" {
		return ""
	}
	b, err := templatesFS.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func Render(source string, data TemplateData) string {
	source = strings.TrimSpace(source)
	if source == "" {
		return ""
	}
	tpl, err := template.New("system_prompt").Option("missingkey=zero").Parse(source)
	if err != nil {
		return source
	}
	var b strings.Builder
	if err := tpl.Execute(&b, data); err != nil {
		return source
	}
	return strings.TrimSpace(b.String())
}

func templatePath(name string) string {
	switch strings.TrimSpace(name) {
	case TemplateChat:
		return "templates/chat_system.md"
	case TemplateProactive:
		return "templates/proactive_system.md"
	default:
		return ""
	}
}
