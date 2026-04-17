package tui

import (
	"database/sql"
	"regexp"
	"strings"
	"time"

	"charm.land/bubbles/v2/cursor"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"tether/internal/store"
)

var toolCallNamePattern = regexp.MustCompile(`^[a-z][a-z0-9._-]*$`)

// chatMessage holds a single chat entry with its role for layout decisions.
type chatMessage struct {
	role              string // user | assistant | system | tool_call
	content           string
	reasoning         string
	reasoningExpanded bool
	streaming         bool
}

type chatModel struct {
	db     *sql.DB
	userID int64
	convID int64

	width  int
	height int

	viewport              viewport.Model
	textarea              textarea.Model
	messages              []chatMessage
	streamingAssistantIdx map[int]int
	streamingToolCalls    map[int]map[string]bool

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
	ConversationID int64
	RequestID      int
	Text           string
	Reasoning      string
	ToolCalls      []toolCallEntry
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
	ta.Placeholder = "Message Tether…"
	ta.SetVirtualCursor(false)
	ta.Focus()
	ta.Prompt = "› "
	ta.CharLimit = 4000
	ta.SetHeight(1)
	// Remove cursor line styling
	s := ta.Styles()
	s.Focused.CursorLine = lipgloss.NewStyle()
	s.Focused.Base = s.Focused.Base.Foreground(lipgloss.Color("255")).Background(colorPanelAlt)
	s.Focused.Placeholder = s.Focused.Placeholder.Foreground(colorDim)
	s.Blurred.Base = s.Blurred.Base.Foreground(lipgloss.Color("255")).Background(colorPanelAlt)
	s.Blurred.Placeholder = s.Blurred.Placeholder.Foreground(colorDim)
	ta.SetStyles(s)
	ta.ShowLineNumbers = false
	// Enter sends a message; keep composing single-line for now.
	ta.KeyMap.InsertNewline.SetEnabled(false)

	vp := viewport.New(viewport.WithWidth(80), viewport.WithHeight(10))
	vp.KeyMap.Left.SetEnabled(false)
	vp.KeyMap.Right.SetEnabled(false)

	return chatModel{
		viewport:              vp,
		textarea:              ta,
		messages:              []chatMessage{},
		streamingAssistantIdx: map[int]int{},
		streamingToolCalls:    map[int]map[string]bool{},
	}
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
	m.width = w
	m.height = h

	innerW := max(20, w)
	transcriptW := max(18, innerW-styleChatTranscript.GetHorizontalFrameSize())
	composerW := max(18, innerW-styleChatComposer.GetHorizontalFrameSize()-styleChatInputBox.GetHorizontalFrameSize())

	bannerH := 1 + styleChatBanner.GetVerticalFrameSize()
	composerH := m.textarea.Height() + 1 + styleChatComposer.GetVerticalFrameSize() + styleChatInputBox.GetVerticalFrameSize()
	viewportH := max(3, h-bannerH-composerH-styleChatTranscript.GetVerticalFrameSize())

	m.viewport.SetWidth(transcriptW)
	m.textarea.SetWidth(composerW)
	m.viewport.SetHeight(viewportH)
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
			role := mm.Role
			if role == "tool_call" && !isValidToolCallContent(mm.Content) {
				role = "system"
			}
			chatMsgs = append(chatMsgs, chatMessage{role: role, content: mm.Content})
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
		case "ctrl+o":
			m = m.toggleLatestReasoning()
			return m, nil
		}

	case cursor.BlinkMsg:
		var cmd tea.Cmd
		m.textarea, cmd = m.textarea.Update(msg)
		return m, cmd

	case tea.MouseClickMsg:
		if msg.Button == tea.MouseLeft && m.clickInTranscript(msg.Y) {
			m = m.toggleLatestReasoning()
			return m, nil
		}
	}

	// Pass through to both viewport and textarea.
	var cmd tea.Cmd
	m.viewport, _ = m.viewport.Update(msg)
	m.textarea, cmd = m.textarea.Update(msg)
	return m, cmd
}

func (m chatModel) View() tea.View {
	innerW := max(20, m.width)
	banner := styleChatBanner.Width(innerW).Render("Tether Chat  •  Enter sends  •  /help for commands  •  $skill for tools")
	transcript := styleChatTranscript.Width(innerW).Render(m.viewport.View())
	inputBox := styleChatInputBox.Width(max(18, innerW-styleChatComposer.GetHorizontalFrameSize())).Render(m.textarea.View())
	composer := styleChatComposer.Width(innerW).Render(inputBox + "\n" + styleChatHint.Render("Enter to send. Use /clear for a fresh session. Ctrl+O toggles separate model reasoning when available."))
	content := lipgloss.JoinVertical(lipgloss.Left, banner, transcript, composer)
	return tea.NewView(content)
}

func (m chatModel) cursor() *tea.Cursor {
	viewportHeight := lipgloss.Height(m.viewport.View())
	c := m.textarea.Cursor()
	if c != nil {
		c.X += 4
		c.Y += 1 + styleChatBanner.GetVerticalFrameSize() + viewportHeight + styleChatTranscript.GetVerticalFrameSize() + 1
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
		lines = append(lines, formatMessage(msg, w))
	}
	m.viewport.SetContent(strings.Join(lines, "\n\n"))
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
	if role == "tool_call" && !isValidToolCallContent(text) {
		role = "system"
	}
	m.messages = append(m.messages, chatMessage{role: role, content: text})
	m.reflow()
	m.viewport.GotoBottom()
	return m
}

func (m chatModel) appendStreamingToolCall(requestID int, name, args string) chatModel {
	if !isValidToolName(name) {
		return m
	}
	key := name + "\x00" + args
	if m.streamingToolCalls[requestID] == nil {
		m.streamingToolCalls[requestID] = map[string]bool{}
	}
	if m.streamingToolCalls[requestID][key] {
		return m
	}
	m.streamingToolCalls[requestID][key] = true
	content := name
	if strings.TrimSpace(args) != "" {
		content += "  " + args
	}
	return m.appendLocal("tool_call", content)
}

func (m chatModel) hasStreamingToolCall(requestID int, name, args string) bool {
	return m.streamingToolCalls[requestID] != nil && m.streamingToolCalls[requestID][name+"\x00"+args]
}

func (m chatModel) startStreamingAssistant(requestID int) chatModel {
	if _, ok := m.streamingAssistantIdx[requestID]; ok {
		return m
	}
	m.messages = append(m.messages, chatMessage{role: "assistant", content: "...", streaming: true})
	m.streamingAssistantIdx[requestID] = len(m.messages) - 1
	m.reflow()
	m.viewport.GotoBottom()
	return m
}

func (m chatModel) setStreamingReasoning(requestID int, text string) chatModel {
	m = m.startStreamingAssistant(requestID)
	idx, ok := m.streamingAssistantIdx[requestID]
	if !ok || idx < 0 || idx >= len(m.messages) {
		return m
	}
	m.messages[idx].reasoning = text
	if strings.TrimSpace(m.messages[idx].content) == "" || m.messages[idx].content == "..." {
		m.messages[idx].reasoningExpanded = true
	}
	m.reflow()
	m.viewport.GotoBottom()
	return m
}

func (m chatModel) setStreamingAssistant(requestID int, text string) chatModel {
	m = m.startStreamingAssistant(requestID)
	idx, ok := m.streamingAssistantIdx[requestID]
	if !ok || idx < 0 || idx >= len(m.messages) {
		return m
	}
	if strings.TrimSpace(text) == "" {
		m.messages[idx].content = "..."
	} else {
		m.messages[idx].content = text
		if strings.TrimSpace(m.messages[idx].reasoning) != "" {
			m.messages[idx].reasoningExpanded = false
		}
	}
	m.reflow()
	m.viewport.GotoBottom()
	return m
}

func (m chatModel) finishStreamingAssistant(requestID int, text string, reasoning string) chatModel {
	m = m.startStreamingAssistant(requestID)
	idx, ok := m.streamingAssistantIdx[requestID]
	if !ok || idx < 0 || idx >= len(m.messages) {
		delete(m.streamingAssistantIdx, requestID)
		delete(m.streamingToolCalls, requestID)
		return m
	}
	if strings.TrimSpace(text) == "" {
		if strings.TrimSpace(m.messages[idx].content) == "" {
			m.messages[idx].content = "..."
		}
	} else {
		m.messages[idx].content = text
	}
	if strings.TrimSpace(reasoning) != "" {
		m.messages[idx].reasoning = reasoning
	}
	m.messages[idx].streaming = false
	if strings.TrimSpace(m.messages[idx].reasoning) != "" {
		m.messages[idx].reasoningExpanded = false
	}
	delete(m.streamingAssistantIdx, requestID)
	delete(m.streamingToolCalls, requestID)
	m.reflow()
	m.viewport.GotoBottom()
	return m
}

func (m chatModel) toggleLatestReasoning() chatModel {
	for i := len(m.messages) - 1; i >= 0; i-- {
		if m.messages[i].role == "assistant" && strings.TrimSpace(m.messages[i].reasoning) != "" && !m.messages[i].streaming {
			m.messages[i].reasoningExpanded = !m.messages[i].reasoningExpanded
			m.reflow()
			return m
		}
	}
	return m
}

func (m chatModel) clickInTranscript(y int) bool {
	if y <= 0 {
		return false
	}
	bodyY := y - 1 // header row
	top := 1 + styleChatBanner.GetVerticalFrameSize()
	bottom := top + lipgloss.Height(m.viewport.View())
	return bodyY >= top && bodyY < bottom
}

// formatMessage renders a single chat message with role-appropriate alignment,
// background, and sender label. width is the current viewport width.
func formatMessage(msg chatMessage, width int) string {
	if width <= 0 {
		width = 80
	}
	bubbleWidth := max(20, min(width-6, 72))

	switch msg.role {
	case "user":
		label := styleSenderUser.Render("You")
		bubble := styleUserMsg.Width(bubbleWidth).Render(label + "\n" + msg.content)
		return lipgloss.PlaceHorizontal(width, lipgloss.Right, bubble)

	case "assistant":
		label := styleSenderBot.Render("Tether")
		body := msg.content
		placeholderBody := strings.TrimSpace(body) == "" || body == "..."
		if placeholderBody {
			body = "..."
		}
		bodyParts := make([]string, 0, 3)
		if strings.TrimSpace(msg.reasoning) != "" {
			if msg.streaming && placeholderBody {
				bodyParts = append(bodyParts, styleDim.Render("Model reasoning"), msg.reasoning)
				body = ""
			} else if msg.streaming {
				bodyParts = append(bodyParts, styleDim.Render("<Model reasoning available. Click or ctrl+o to expand>"))
			} else if msg.reasoningExpanded {
				bodyParts = append(bodyParts, styleDim.Render("Model reasoning  <click or ctrl+o to collapse>"), msg.reasoning)
			} else {
				bodyParts = append(bodyParts, styleDim.Render("<Model reasoning available. Click or ctrl+o to expand>"))
			}
		} else if !msg.streaming {
			bodyParts = append(bodyParts, styleDim.Render("<No separate model reasoning returned>"))
		}
		if strings.TrimSpace(body) != "" {
			bodyParts = append(bodyParts, body)
		}
		body = strings.Join(bodyParts, "\n\n")
		bubble := styleAgentMsg.Width(bubbleWidth).Render(label + "\n" + body)
		return lipgloss.PlaceHorizontal(width, lipgloss.Left, bubble)

	case "tool_call":
		if !isValidToolCallContent(msg.content) {
			styled := styleSystemMsg.Render("notice  " + msg.content)
			return lipgloss.PlaceHorizontal(width, lipgloss.Center, styled)
		}
		styled := styleToolMsg.Width(max(18, min(width-10, 64))).Render("tool  " + msg.content)
		return lipgloss.PlaceHorizontal(width, lipgloss.Center, styled)

	default: // system
		styled := styleSystemMsg.Render("notice  " + msg.content)
		return lipgloss.PlaceHorizontal(width, lipgloss.Center, styled)
	}
}

func isValidToolName(name string) bool {
	return toolCallNamePattern.MatchString(strings.TrimSpace(name))
}

func isValidToolCallContent(content string) bool {
	name := strings.TrimSpace(content)
	if name == "" {
		return false
	}
	if i := strings.IndexAny(name, " \t"); i >= 0 {
		name = name[:i]
	}
	return isValidToolName(name)
}
