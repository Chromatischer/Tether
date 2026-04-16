package tui

import (
	"database/sql"
	"strings"
	"time"

	"charm.land/bubbles/v2/cursor"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"tether/internal/store"
)

// chatMessage holds a single chat entry with its role for layout decisions.
type chatMessage struct {
	role    string // user | assistant | system | tool_call
	content string
}

type chatModel struct {
	db     *sql.DB
	userID int64
	convID int64

	viewport viewport.Model
	textarea textarea.Model
	messages []chatMessage

	polling bool
	err     error
}

type chatSendMsg struct {
	Text string
}

// toolCallEntry holds one tool call for display, mirroring agent.ToolCallInfo
// without importing the agent package into the TUI.
type toolCallEntry struct {
	Name string
	Args string
}

type agentReplyMsg struct {
	Text      string
	ToolCalls []toolCallEntry
}

type loginSuccessMsg struct {
	User *store.User
	Conv *store.Conversation
}

type chatLoadedMsg struct {
	Messages []chatMessage
}

type chatPollNotificationsMsg struct{}

type chatNotificationsDeliveredMsg struct {
	Lines []chatMessage
}

func newChatModel() chatModel {
	ta := textarea.New()
	ta.Placeholder = "Message…"
	ta.SetVirtualCursor(false)
	ta.Focus()
	ta.Prompt = "┃ "
	ta.CharLimit = 4000
	ta.SetHeight(3)
	// Remove cursor line styling
	s := ta.Styles()
	s.Focused.CursorLine = lipgloss.NewStyle()
	ta.SetStyles(s)
	ta.ShowLineNumbers = false
	// Enter sends a message; keep composing single-line for now.
	ta.KeyMap.InsertNewline.SetEnabled(false)

	vp := viewport.New(viewport.WithWidth(80), viewport.WithHeight(10))
	vp.KeyMap.Left.SetEnabled(false)
	vp.KeyMap.Right.SetEnabled(false)

	return chatModel{viewport: vp, textarea: ta, messages: []chatMessage{}}
}

func (m chatModel) withConversation(db *sql.DB, userID, convID int64) chatModel {
	m.db = db
	m.userID = userID
	m.convID = convID
	m.polling = false
	return m
}

func (m chatModel) withSize(w, h int) chatModel {
	if w <= 0 || h <= 0 {
		return m
	}
	m.viewport.SetWidth(w)
	m.textarea.SetWidth(w)
	// Reserve 1 extra line for the divider between viewport and textarea.
	m.viewport.SetHeight(max(1, h-m.textarea.Height()-1))
	m.reflow()
	m.viewport.GotoBottom()
	return m
}

func (m chatModel) loadCmd() tea.Cmd {
	if m.db == nil || m.convID == 0 {
		return nil
	}
	db := m.db
	convID := m.convID
	return func() tea.Msg {
		msgs, err := store.ListRecentMessages(db, convID, 200)
		if err != nil {
			return authStatusMsg{Text: "failed to load messages: " + err.Error(), IsErr: true}
		}
		chatMsgs := make([]chatMessage, 0, len(msgs))
		for _, mm := range msgs {
			chatMsgs = append(chatMsgs, chatMessage{role: mm.Role, content: mm.Content})
		}
		return chatLoadedMsg{Messages: chatMsgs}
	}
}

func (m chatModel) pollTickCmd() tea.Cmd {
	if m.db == nil || m.userID == 0 || m.convID == 0 {
		return nil
	}
	return tea.Tick(3*time.Second, func(time.Time) tea.Msg { return chatPollNotificationsMsg{} })
}

func (m chatModel) pollNotificationsCmd() tea.Cmd {
	if m.db == nil || m.userID == 0 || m.convID == 0 {
		return nil
	}
	db := m.db
	userID := m.userID
	convID := m.convID
	return func() tea.Msg {
		nots, err := store.ListUndeliveredNotifications(db, userID, 50)
		if err != nil || len(nots) == 0 {
			return chatNotificationsDeliveredMsg{}
		}
		lines := make([]chatMessage, 0, len(nots))
		for _, n := range nots {
			text := "[Proactive/" + n.Kind + "] " + n.Content
			_ = store.AddMessage(db, convID, "assistant", text)
			_ = store.MarkNotificationDelivered(db, n.ID)
			lines = append(lines, chatMessage{role: "assistant", content: text})
		}
		return chatNotificationsDeliveredMsg{Lines: lines}
	}
}

func (m chatModel) Update(msg tea.Msg) (chatModel, tea.Cmd) {
	switch msg := msg.(type) {
	case chatLoadedMsg:
		m.messages = msg.Messages
		m.reflow()
		m.viewport.GotoBottom()
		if !m.polling {
			m.polling = true
			return m, m.pollTickCmd()
		}
		return m, nil

	case chatPollNotificationsMsg:
		return m, m.pollNotificationsCmd()

	case chatNotificationsDeliveredMsg:
		if len(msg.Lines) > 0 {
			m.messages = append(m.messages, msg.Lines...)
			m.reflow()
			m.viewport.GotoBottom()
		}
		return m, m.pollTickCmd()

	case tea.WindowSizeMsg:
		m = m.withSize(msg.Width, msg.Height)
		return m, nil

	case tea.KeyPressMsg:
		switch msg.String() {
		case "enter":
			// Send message on enter unless user is composing a multiline message.
			text := strings.TrimSpace(m.textarea.Value())
			if text == "" {
				return m, nil
			}
			m.textarea.Reset()
			return m, func() tea.Msg { return chatSendMsg{Text: text} }
		}

	case cursor.BlinkMsg:
		var cmd tea.Cmd
		m.textarea, cmd = m.textarea.Update(msg)
		return m, cmd
	}

	// Pass through to both viewport and textarea.
	var cmd tea.Cmd
	m.viewport, _ = m.viewport.Update(msg)
	m.textarea, cmd = m.textarea.Update(msg)
	return m, cmd
}

func (m chatModel) View() tea.View {
	vpView := m.viewport.View()
	sep := styleDivider.Render(strings.Repeat("─", m.viewport.Width()))
	return tea.NewView(vpView + "\n" + sep + "\n" + m.textarea.View())
}

func (m chatModel) cursor() *tea.Cursor {
	viewportHeight := lipgloss.Height(m.viewport.View())
	c := m.textarea.Cursor()
	if c != nil {
		c.Y += viewportHeight + 1 // +1 for divider line
	}
	return c
}

func (m *chatModel) reflow() {
	if m.viewport.Width() <= 0 {
		return
	}
	w := m.viewport.Width()
	lines := make([]string, 0, len(m.messages))
	for _, msg := range m.messages {
		lines = append(lines, formatMessage(msg.role, msg.content, w))
	}
	m.viewport.SetContent(strings.Join(lines, "\n"))
}

func (m chatModel) appendLocal(sender, text string) chatModel {
	var role string
	switch sender {
	case "You":
		role = "user"
	case "Tether":
		role = "assistant"
	case "tool_call":
		role = "tool_call"
	default:
		role = "system"
	}
	m.messages = append(m.messages, chatMessage{role: role, content: text})
	m.reflow()
	m.viewport.GotoBottom()
	return m
}

// formatMessage renders a single chat message with role-appropriate alignment,
// background, and sender label. width is the current viewport width.
func formatMessage(role, content string, width int) string {
	if width <= 0 {
		width = 80
	}

	switch role {
	case "user":
		label := styleSenderUser.Render("you")
		inner := label + "  " + content
		return styleUserMsg.Width(width).Align(lipgloss.Right).Render(inner)

	case "assistant":
		label := styleSenderBot.Render("tether")
		inner := label + "  " + content
		return styleAgentMsg.Width(width).Render(inner)

	case "tool_call":
		styled := styleToolMsg.Render("⚙ " + content)
		return lipgloss.PlaceHorizontal(width, lipgloss.Center, styled)

	default: // system
		styled := styleSenderSystem.Render(content)
		return lipgloss.PlaceHorizontal(width, lipgloss.Center, styled)
	}
}
