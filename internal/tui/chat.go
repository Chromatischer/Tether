package tui

import (
	"database/sql"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"charm.land/bubbles/v2/cursor"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"tether/internal/skills"
	"tether/internal/store"
	"tether/internal/userspace"
)

var toolCallNamePattern = regexp.MustCompile(`^[a-z][a-z0-9._-]*$`)

type composerFocus int

const (
	composerFocusInput composerFocus = iota
	composerFocusSuggestion
	composerFocusSend
)

type chatSuggestion struct {
	Label       string
	InsertValue string
	Detail      string
}

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
	streamingToolCalls    map[int]map[string]int
	dataDir               string
	isAdmin               bool
	skills                []string
	focus                 composerFocus
	suggestions           []chatSuggestion
	selectedSuggestion    int
	autocompleteDismissed bool

	polling bool
	err     error
}

type chatSendMsg struct {
	Text string
}

// toolCallEntry holds one tool call for display, mirroring agent.ToolCallInfo
// without importing the agent package into the TUI.
type toolCallEntry struct {
	Name   string
	Args   string
	Result string
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
		streamingToolCalls:    map[int]map[string]int{},
	}
}

func (m chatModel) withConversation(db *sql.DB, userID, convID int64) chatModel {
	m.db = db
	m.userID = userID
	m.convID = convID
	m.polling = false
	m.reloadSkills()
	m.syncAutocomplete()
	return m
}

func (m chatModel) withComposerContext(dataDir string, isAdmin bool) chatModel {
	m.dataDir = dataDir
	m.isAdmin = isAdmin
	m.reloadSkills()
	m.syncAutocomplete()
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
	composerW := max(18, innerW-styleChatComposer.GetHorizontalFrameSize())
	inputW := max(18, composerW-styleChatInputBox.GetHorizontalFrameSize())

	bannerH := 1 + styleChatBanner.GetVerticalFrameSize()
	m.textarea.SetWidth(inputW)
	composerH := m.composerHeight(innerW)
	viewportH := max(3, h-bannerH-composerH-styleChatTranscript.GetVerticalFrameSize())

	m.viewport.SetWidth(transcriptW)
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
			targetConvID := convID
			if n.ConversationID != 0 {
				targetConvID = n.ConversationID
			}
			text := "[Proactive/" + n.Kind + "] " + n.Content
			_ = store.AddMessage(db, targetConvID, "assistant", text)
			_ = store.MarkNotificationDelivered(db, n.ID)
			if targetConvID == convID {
				lines = append(lines, chatMessage{role: "assistant", content: text})
			}
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
		case "tab":
			var handled bool
			m, handled = m.handleTab(false)
			if handled {
				return m, nil
			}
		case "shift+tab":
			var handled bool
			m, handled = m.handleTab(true)
			if handled {
				return m, nil
			}
		case "enter":
			if len(m.suggestions) > 0 {
				switch m.focus {
				case composerFocusInput:
					m = m.applySuggestion(0)
					return m, nil
				case composerFocusSuggestion:
					m = m.applySuggestion(m.selectedSuggestion)
					return m, nil
				case composerFocusSend:
					var cmd tea.Cmd
					m, cmd = m.sendCmd()
					if cmd != nil {
						return m, cmd
					}
					return m, nil
				}
			}
			if m.focus == composerFocusSend {
				var cmd tea.Cmd
				m, cmd = m.sendCmd()
				if cmd != nil {
					return m, cmd
				}
				return m, nil
			}
			var cmd tea.Cmd
			m, cmd = m.sendCmd()
			if cmd != nil {
				return m, cmd
			}
			return m, nil
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
	prevValue := m.textarea.Value()
	m.textarea, cmd = m.textarea.Update(msg)
	if prevValue != m.textarea.Value() {
		m.autocompleteDismissed = false
		m.syncAutocomplete()
	}
	return m, cmd
}

func (m chatModel) View() tea.View {
	innerW := max(20, m.width)
	banner := styleChatBanner.Width(innerW).Render("Tether Chat  •  TAB for autocomplete  •  /help for commands  •  $skill for tools")
	transcript := styleChatTranscript.Width(innerW).Render(m.viewport.View())
	composerW := max(18, innerW-styleChatComposer.GetHorizontalFrameSize())
	inputBox := styleChatInputBox.Width(composerW).Render(m.textarea.View())
	if rendered := m.renderSuggestions(composerW); rendered != "" {
		inputBox = rendered + "\n" + inputBox
	}
	composerBody := inputBox + "\n" + styleChatHint.Render("Tab cycles suggestions, then send. Enter applies a suggestion or sends once the send step is focused. Use /clear for a fresh session. Ctrl+O toggles separate model reasoning when available.")
	composer := styleChatComposer.Width(innerW).Render(composerBody)
	content := lipgloss.JoinVertical(lipgloss.Left, banner, transcript, composer)
	return tea.NewView(content)
}

func (m chatModel) cursor() *tea.Cursor {
	if m.focus != composerFocusInput {
		return nil
	}
	viewportHeight := lipgloss.Height(m.viewport.View())
	c := m.textarea.Cursor()
	if c != nil {
		c.X += 1
		c.Y += 1 + styleChatBanner.GetVerticalFrameSize() + viewportHeight + styleChatTranscript.GetVerticalFrameSize() + 1 + m.suggestionsRenderHeight(m.composerContentWidth())
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
	return m.upsertStreamingToolCall(requestID, toolCallEntry{Name: name, Args: args})
}

func (m chatModel) upsertStreamingToolCall(requestID int, entry toolCallEntry) chatModel {
	if !isValidToolName(entry.Name) {
		return m
	}
	key := toolCallKey(entry.Name, entry.Args)
	if m.streamingToolCalls[requestID] == nil {
		m.streamingToolCalls[requestID] = map[string]int{}
	}
	if idx, ok := m.streamingToolCalls[requestID][key]; ok {
		if idx >= 0 && idx < len(m.messages) {
			m.messages[idx].content = formatToolCallContent(entry)
			m.reflow()
			m.viewport.GotoBottom()
		}
		return m
	}
	m = m.appendLocal("tool_call", formatToolCallContent(entry))
	m.streamingToolCalls[requestID][key] = len(m.messages) - 1
	return m
}

func (m chatModel) hasStreamingToolCall(requestID int, name, args string) bool {
	_, ok := m.streamingToolCalls[requestID][toolCallKey(name, args)]
	return ok
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
		bodyWidth := max(16, bubbleWidth-2)
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
			if placeholderBody {
				bodyParts = append(bodyParts, body)
			} else {
				bodyParts = append(bodyParts, renderRichText(body, bodyWidth, richTextAssistant))
			}
		}
		body = strings.Join(bodyParts, "\n\n")
		bubble := styleAgentMsg.Width(bubbleWidth).Render(label + "\n" + body)
		return lipgloss.PlaceHorizontal(width, lipgloss.Left, bubble)

	case "tool_call":
		if !isValidToolCallContent(msg.content) {
			styled := styleSystemMsg.Render(msg.content)
			return lipgloss.PlaceHorizontal(width, lipgloss.Center, styled)
		}
		entry, _ := parseToolCallContent(msg.content)
		body := "tool  " + entry.Name
		if strings.TrimSpace(entry.Args) != "" {
			body += "  " + entry.Args
		}
		if strings.TrimSpace(entry.Result) != "" {
			body += "\n\n" + entry.Result
		}
		styled := styleToolMsg.Width(max(18, min(width-10, 64))).Render(body)
		return lipgloss.PlaceHorizontal(width, lipgloss.Center, styled)

	default: // system
		rendered := renderRichText(msg.content, max(16, min(width-10, 64)-2), richTextSystem)
		styled := styleSystemMsg.Width(max(18, min(width-10, 64))).Render(rendered)
		return lipgloss.PlaceHorizontal(width, lipgloss.Center, styled)
	}
}

func isValidToolName(name string) bool {
	return toolCallNamePattern.MatchString(strings.TrimSpace(name))
}

func isValidToolCallContent(content string) bool {
	entry, ok := parseToolCallContent(content)
	return ok && isValidToolName(entry.Name)
}

func toolCallKey(name, args string) string {
	return strings.TrimSpace(name) + "\x00" + strings.TrimSpace(args)
}

func formatToolCallContent(entry toolCallEntry) string {
	line := strings.TrimSpace(entry.Name)
	if args := strings.TrimSpace(entry.Args); args != "" {
		line += "  " + args
	}
	if result := strings.TrimSpace(entry.Result); result != "" {
		return line + "\n\n" + result
	}
	return line
}

func parseToolCallContent(content string) (toolCallEntry, bool) {
	content = strings.TrimSpace(content)
	if content == "" {
		return toolCallEntry{}, false
	}
	head := content
	body := ""
	if i := strings.Index(content, "\n"); i >= 0 {
		head = strings.TrimSpace(content[:i])
		body = strings.TrimLeft(content[i+1:], "\n")
	}
	if head == "" {
		return toolCallEntry{}, false
	}
	entry := toolCallEntry{Name: head}
	if i := strings.IndexAny(head, " \t"); i >= 0 {
		entry.Name = strings.TrimSpace(head[:i])
		entry.Args = strings.TrimSpace(head[i+1:])
	}
	entry.Result = strings.TrimSpace(body)
	return entry, true
}

func (m *chatModel) reloadSkills() {
	m.skills = nil
	if m.dataDir == "" || m.userID == 0 {
		return
	}
	mgr := skills.NewManager()
	list, err := mgr.List(userspace.ForUser(m.dataDir, m.userID))
	if err != nil {
		return
	}
	names := make([]string, 0, len(list))
	for _, s := range list {
		if !s.UserInvocable {
			continue
		}
		names = append(names, s.Name)
	}
	sort.Strings(names)
	m.skills = names
}

func (m *chatModel) syncAutocomplete() {
	if m.autocompleteDismissed {
		m.suggestions = nil
		m.selectedSuggestion = 0
		if m.focus == composerFocusSuggestion {
			m.focus = composerFocusInput
		}
		if m.width > 0 && m.height > 0 {
			*m = m.withSize(m.width, m.height)
		}
		return
	}
	suggestions := m.matchSuggestions(strings.TrimSpace(m.textarea.Value()))
	m.suggestions = suggestions
	if len(suggestions) == 0 {
		if m.focus == composerFocusSuggestion {
			m.focus = composerFocusInput
		}
		m.selectedSuggestion = 0
		if m.width > 0 && m.height > 0 {
			*m = m.withSize(m.width, m.height)
		}
		return
	}
	if m.selectedSuggestion >= len(suggestions) {
		m.selectedSuggestion = len(suggestions) - 1
	}
	if m.width > 0 && m.height > 0 {
		*m = m.withSize(m.width, m.height)
	}
}

func (m chatModel) handleTab(reverse bool) (chatModel, bool) {
	if len(m.suggestions) == 0 {
		if reverse {
			if m.focus == composerFocusInput {
				m.focus = composerFocusSend
			} else {
				m.focus = composerFocusInput
			}
			return m, true
		}
		if m.focus == composerFocusInput {
			m.focus = composerFocusSend
		} else {
			m.focus = composerFocusInput
		}
		return m, true
	}

	if reverse {
		switch m.focus {
		case composerFocusInput:
			m.focus = composerFocusSend
		case composerFocusSuggestion:
			if m.selectedSuggestion == 0 {
				m.focus = composerFocusInput
			} else {
				m.selectedSuggestion--
			}
		case composerFocusSend:
			m.focus = composerFocusSuggestion
			m.selectedSuggestion = len(m.suggestions) - 1
		}
		return m, true
	}

	switch m.focus {
	case composerFocusInput:
		m.focus = composerFocusSuggestion
		m.selectedSuggestion = 0
	case composerFocusSuggestion:
		if m.selectedSuggestion >= len(m.suggestions)-1 {
			m.focus = composerFocusSend
		} else {
			m.selectedSuggestion++
		}
	case composerFocusSend:
		m.focus = composerFocusInput
	}
	return m, true
}

func (m chatModel) applySuggestion(idx int) chatModel {
	if idx < 0 || idx >= len(m.suggestions) {
		return m
	}
	m.textarea.SetValue(m.suggestions[idx].InsertValue)
	m.focus = composerFocusInput
	m.autocompleteDismissed = true
	m.syncAutocomplete()
	return m
}

func (m chatModel) sendCmd() (chatModel, tea.Cmd) {
	text := strings.TrimSpace(m.textarea.Value())
	if text == "" {
		return m, nil
	}
	m.textarea.Reset()
	m.focus = composerFocusInput
	m.autocompleteDismissed = false
	m.syncAutocomplete()
	return m, func() tea.Msg { return chatSendMsg{Text: text} }
}

func (m chatModel) renderSuggestions(width int) string {
	if len(m.suggestions) == 0 {
		return ""
	}
	rows := make([]string, 0, len(m.suggestions))
	for i, s := range m.suggestions {
		line := s.Label
		if strings.TrimSpace(s.Detail) != "" {
			line += "  " + styleDim.Render(s.Detail)
		}
		style := styleAutocompleteSuggestion.Width(width)
		if m.focus == composerFocusSuggestion && i == m.selectedSuggestion {
			style = styleAutocompleteSuggestionActive.Width(width)
		}
		rows = append(rows, style.Render(line))
	}
	return lipgloss.JoinVertical(lipgloss.Left, rows...)
}

func (m chatModel) suggestionsHeight(width int) int {
	rendered := m.renderSuggestions(width)
	if rendered == "" {
		return 0
	}
	return lipgloss.Height(rendered) + 1
}

func (m chatModel) suggestionsRenderHeight(width int) int {
	rendered := m.renderSuggestions(width)
	if rendered == "" {
		return 0
	}
	return lipgloss.Height(rendered)
}

func (m chatModel) composerHeight(innerW int) int {
	composerW := max(18, innerW-styleChatComposer.GetHorizontalFrameSize())
	body := styleChatInputBox.Width(composerW).Render(m.textarea.View()) + "\n" + styleChatHint.Render("Tab cycles suggestions, then send. Enter applies a suggestion or sends once the send step is focused. Use /clear for a fresh session. Ctrl+O toggles separate model reasoning when available.")
	if rendered := m.renderSuggestions(composerW); rendered != "" {
		body = rendered + "\n" + body
	}
	return lipgloss.Height(styleChatComposer.Width(innerW).Render(body))
}

func (m chatModel) composerContentWidth() int {
	innerW := max(20, m.width)
	return max(18, innerW-styleChatComposer.GetHorizontalFrameSize())
}

func (m chatModel) matchSuggestions(text string) []chatSuggestion {
	switch {
	case strings.HasPrefix(text, "/"):
		return matchCommandSuggestions(text, m.isAdmin)
	case strings.HasPrefix(text, "$"):
		return matchSkillSuggestions(text, m.skills)
	default:
		return nil
	}
}

func matchCommandSuggestions(text string, isAdmin bool) []chatSuggestion {
	candidates := []chatSuggestion{
		{Label: "/help", InsertValue: "/help", Detail: "show available commands"},
		{Label: "/clear", InsertValue: "/clear", Detail: "start a fresh conversation"},
		{Label: "/resume", InsertValue: "/resume ", Detail: "resume a previous conversation"},
		{Label: "/logout", InsertValue: "/logout", Detail: "log out of the SSH portal"},
		{Label: "/confirm", InsertValue: "/confirm ", Detail: "approve a pending tool confirmation"},
		{Label: "/tools list", InsertValue: "/tools list", Detail: "list available tools"},
		{Label: "/tools search", InsertValue: "/tools search ", Detail: "search tools"},
		{Label: "/tools describe", InsertValue: "/tools describe ", Detail: "inspect one tool"},
		{Label: "/subagent spawn", InsertValue: "/subagent spawn ", Detail: "start a background subagent"},
		{Label: "/subagent status", InsertValue: "/subagent status ", Detail: "check subagent progress"},
		{Label: "/proactive action", InsertValue: "/proactive action ", Detail: "run a proactive action"},
		{Label: "/proactive agent", InsertValue: "/proactive agent ", Detail: "inspect a proactive agent"},
		{Label: "/signal link", InsertValue: "/signal link", Detail: "link Signal"},
		{Label: "/signal status", InsertValue: "/signal status", Detail: "show Signal link state"},
		{Label: "/signal unlink", InsertValue: "/signal unlink", Detail: "unlink Signal"},
		{Label: "/discord status", InsertValue: "/discord status", Detail: "show Discord link state"},
		{Label: "/discord link", InsertValue: "/discord link ", Detail: "link Discord with a code"},
		{Label: "/discord unlink", InsertValue: "/discord unlink", Detail: "unlink Discord"},
		{Label: "/memory list", InsertValue: "/memory list ", Detail: "list memory items"},
		{Label: "/memory add", InsertValue: "/memory add ", Detail: "store a memory item"},
		{Label: "/memory update", InsertValue: "/memory update ", Detail: "update a memory item"},
		{Label: "/memory delete", InsertValue: "/memory delete ", Detail: "delete a memory item"},
		{Label: "/task list", InsertValue: "/task list", Detail: "list tasks"},
		{Label: "/task add", InsertValue: "/task add ", Detail: "add a task"},
		{Label: "/task edit", InsertValue: "/task edit ", Detail: "edit a task"},
		{Label: "/task done", InsertValue: "/task done ", Detail: "mark a task done"},
		{Label: "/secret add", InsertValue: "/secret add ", Detail: "store a secret"},
		{Label: "/secret list", InsertValue: "/secret list", Detail: "list stored secret labels"},
		{Label: "/secret delete", InsertValue: "/secret delete ", Detail: "delete a secret"},
		{Label: "/secret clear", InsertValue: "/secret clear", Detail: "remove all secrets"},
	}
	if isAdmin {
		candidates = append(candidates,
			chatSuggestion{Label: "/admin users list", InsertValue: "/admin users list", Detail: "list users"},
			chatSuggestion{Label: "/admin users promote", InsertValue: "/admin users promote ", Detail: "grant admin"},
			chatSuggestion{Label: "/admin users demote", InsertValue: "/admin users demote ", Detail: "remove admin"},
			chatSuggestion{Label: "/admin audit tail", InsertValue: "/admin audit tail ", Detail: "tail audit events"},
			chatSuggestion{Label: "/admin signal status", InsertValue: "/admin signal status", Detail: "check Signal backend"},
			chatSuggestion{Label: "/admin jobs status", InsertValue: "/admin jobs status", Detail: "check background jobs"},
		)
	}
	return filterSuggestions(candidates, text)
}

func matchSkillSuggestions(text string, skills []string) []chatSuggestion {
	if len(skills) == 0 {
		return nil
	}
	candidates := make([]chatSuggestion, 0, len(skills))
	for _, name := range skills {
		candidates = append(candidates, chatSuggestion{
			Label:       "$" + name,
			InsertValue: "$" + name + " ",
			Detail:      "invoke skill",
		})
	}
	return filterSuggestions(candidates, text)
}

func filterSuggestions(candidates []chatSuggestion, query string) []chatSuggestion {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" || q == "/" || q == "$" {
		return slices.Clone(candidates)
	}
	out := make([]chatSuggestion, 0, len(candidates))
	for _, c := range candidates {
		label := strings.ToLower(c.Label)
		if strings.HasPrefix(label, q) {
			out = append(out, c)
		}
	}
	return out
}
