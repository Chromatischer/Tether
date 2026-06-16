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
	role      string // user | assistant | assistant_reasoning | assistant_pending | system | tool_call
	content   string
	streaming bool
	requestID int
	dbID      int64
	expanded  bool
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
	streamFrame           int // drives streaming dot animation
	pendingAssistantIdx   map[int]int
	streamingToolCalls    map[int][]int
	streamStates          map[int]streamState
	dataDir               string
	isAdmin               bool
	skills                []string
	focus                 composerFocus
	suggestions           []chatSuggestion
	selectedSuggestion    int
	autocompleteDismissed bool
	rowHits               []chatRowHit

	polling bool
	err     error
	term    TerminalProfile
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
	// ReasoningItems is the signed reasoning behind the answer; Model is the
	// model that produced it. Persisted so reasoning can be replayed on later
	// turns (see store.SetMessageReasoning).
	ReasoningItems []store.ReasoningBlock
	Model          string
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

type streamTickMsg struct{}

type streamState struct {
	lastReasoning string
	lastText      string
}

type chatRowHit struct {
	startLine int
	endLine   int
	msgIndex  int
}

func newChatModel() chatModel {
	ta := textarea.New()
	ta.Placeholder = "Message Tether…"
	ta.SetVirtualCursor(false)
	ta.Focus()
	ta.Prompt = ""
	ta.CharLimit = 4000
	ta.SetHeight(2)
	// Remove cursor line styling
	s := ta.Styles()
	s.Focused.CursorLine = lipgloss.NewStyle()
	s.Focused.Base = s.Focused.Base.Foreground(lipgloss.Color("255")).Background(colorHeaderBg)
	s.Focused.Placeholder = s.Focused.Placeholder.Foreground(colorDim)
	s.Blurred.Base = s.Blurred.Base.Foreground(lipgloss.Color("255")).Background(colorHeaderBg)
	s.Blurred.Placeholder = s.Blurred.Placeholder.Foreground(colorDim)
	ta.SetStyles(s)
	ta.ShowLineNumbers = false
	// Enter sends a message; keep composing single-line for now.
	ta.KeyMap.InsertNewline.SetEnabled(false)

	vp := viewport.New(viewport.WithWidth(80), viewport.WithHeight(10))
	vp.KeyMap.Left.SetEnabled(false)
	vp.KeyMap.Right.SetEnabled(false)

	return chatModel{
		viewport:            vp,
		textarea:            ta,
		messages:            []chatMessage{},
		pendingAssistantIdx: map[int]int{},
		streamingToolCalls:  map[int][]int{},
		streamStates:        map[int]streamState{},
	}
}

func (m chatModel) hasStreamingMessages() bool {
	for _, msg := range m.messages {
		if msg.streaming {
			return true
		}
	}
	return false
}

func (m chatModel) streamTickCmd() tea.Cmd {
	return tea.Tick(120*time.Millisecond, func(time.Time) tea.Msg {
		return streamTickMsg{}
	})
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

func (m chatModel) withTerminalProfile(term TerminalProfile) chatModel {
	m.term = term
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

	promptW := lipgloss.Width(styleChatPrompt.Render("❯") + " ")
	inputInnerW := max(10, composerW-promptW-styleChatInputBox.GetHorizontalFrameSize())
	m.textarea.SetWidth(inputInnerW)
	composerH := m.composerHeight(innerW)
	viewportH := max(3, h-composerH-styleChatTranscript.GetVerticalFrameSize())

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
		chatMsgs, err := loadChatMessages(db, convID, 200)
		if err != nil {
			return authStatusMsg{Text: "failed to load messages: " + err.Error(), IsErr: true}
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
			_ = store.AddMessage(db, targetConvID, "system", text)
			_ = store.MarkNotificationDelivered(db, n.ID)
			if targetConvID == convID {
				lines = append(lines, chatMessage{role: "system", content: text})
			}
		}
		return chatNotificationsDeliveredMsg{Lines: lines}
	}
}

func (m chatModel) Update(msg tea.Msg) (chatModel, tea.Cmd) {
	switch msg := msg.(type) {
	case chatLoadedMsg:
		m.messages = preserveLoadedMessageState(m.messages, msg.Messages)
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

	case streamTickMsg:
		if m.hasStreamingMessages() {
			m.streamFrame = (m.streamFrame + 1) % 4
			m.reflow()
			return m, m.streamTickCmd()
		}
		return m, nil

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
			return m, nil
		}

	case cursor.BlinkMsg:
		var cmd tea.Cmd
		m.textarea, cmd = m.textarea.Update(msg)
		return m, cmd

	case tea.MouseClickMsg:
		if msg.Button == tea.MouseLeft {
			m = m.toggleExpandableAt(msg.Y)
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

	transcript := styleChatTranscript.Width(innerW).Render(m.viewport.View())

	composerW := max(18, innerW-styleChatComposer.GetHorizontalFrameSize())

	prompt := styleChatPrompt.Render("❯")
	promptW := lipgloss.Width(prompt + " ")
	inputInnerW := max(10, composerW-promptW-styleChatInputBox.GetHorizontalFrameSize())
	inputBox := styleChatInputBox.Width(inputInnerW).Render(m.textarea.View())
	inputRow := styleChatRow.Width(composerW).Render(prompt + styleChatPromptGap.Render(" ") + inputBox)

	hintParts := []string{
		styleChatHintKey.Render("tab"),
		styleChatHintText.Render(" cycle"),
		styleChatHintGap.Render("  "),
		styleChatHintKey.Render("↵"),
		styleChatHintText.Render(" apply"),
		styleChatHintGap.Render("  "),
		styleChatHintKey.Render("esc"),
		styleChatHintText.Render(" dismiss"),
		styleChatHintGap.Render("  "),
	}
	hintsLine := styleChatRow.Width(composerW).Render(lipgloss.JoinHorizontal(lipgloss.Top, hintParts...))

	emptyLine := styleChatRow.Width(composerW).Render("")
	composerBody := emptyLine + "\n" + inputRow + "\n" + hintsLine
	if rendered := m.renderSuggestions(composerW); rendered != "" {
		composerBody = rendered + "\n" + composerBody
	}
	composer := styleChatComposer.Width(innerW).Render(composerBody)

	content := lipgloss.JoinVertical(lipgloss.Left, transcript, composer)
	return tea.NewView(content)
}

func (m chatModel) cursor() *tea.Cursor {
	if m.focus != composerFocusInput {
		return nil
	}
	viewportHeight := lipgloss.Height(m.viewport.View())
	c := m.textarea.Cursor()
	if c != nil {
		composerLeftPad := styleChatComposer.GetHorizontalFrameSize() / 2 // Padding(0,1) → 1 left
		inputBoxLeftPad := styleChatInputBox.GetHorizontalFrameSize() / 2
		c.X += composerLeftPad + lipgloss.Width(styleChatPrompt.Render("❯")+" ") + inputBoxLeftPad
		c.Y += viewportHeight + styleChatTranscript.GetVerticalFrameSize() + 1 + m.suggestionsRenderHeight(m.composerContentWidth())
	}
	return c
}

func (m *chatModel) reflow() {
	if m.viewport.Width() <= 0 {
		return
	}
	w := m.viewport.Width()
	lines := make([]string, 0, len(m.messages))
	hits := make([]chatRowHit, 0, len(m.messages))
	lineCursor := 0
	for i, msg := range m.messages {
		rendered := formatMessage(msg, w, m.streamFrame, m.term)
		lines = append(lines, rendered)
		hits = append(hits, chatRowHit{
			startLine: lineCursor,
			endLine:   lineCursor + max(1, lipgloss.Height(rendered)),
			msgIndex:  i,
		})
		lineCursor += max(1, lipgloss.Height(rendered)) + 1
	}
	m.rowHits = hits
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
	m.messages = append(m.messages, chatMessage{role: m.normalizeRole(role, text), content: text})
	m.reflow()
	m.viewport.GotoBottom()
	return m
}

func (m chatModel) latestPersistedMessageID() int64 {
	var latest int64
	for _, msg := range m.messages {
		if msg.dbID > latest {
			latest = msg.dbID
		}
	}
	return latest
}

func (m chatModel) normalizeRole(role, text string) string {
	if role == "tool_call" && !isValidToolCallContent(text) {
		return "system"
	}
	if role == "assistant_text" {
		return "assistant"
	}
	return role
}

func (m *chatModel) ensureStreamState(requestID int) {
	if m.streamStates == nil {
		m.streamStates = map[int]streamState{}
	}
	if _, ok := m.streamStates[requestID]; !ok {
		m.streamStates[requestID] = streamState{}
	}
}

func (m chatModel) appendMessage(msg chatMessage) chatModel {
	msg.role = m.normalizeRole(msg.role, msg.content)
	if msg.role == "assistant_reasoning" || msg.role == "tool_call" {
		msg.expanded = true
	}
	if msg.role != "assistant_pending" && msg.dbID == 0 && msg.requestID != 0 && m.db != nil && m.convID != 0 {
		if id, err := store.AddMessageID(m.db, m.convID, msg.role, msg.content); err == nil {
			msg.dbID = id
		}
	}
	m.messages = append(m.messages, msg)
	m.reflow()
	m.viewport.GotoBottom()
	return m
}

func (m chatModel) updateMessageContent(idx int, content string) chatModel {
	if idx < 0 || idx >= len(m.messages) {
		return m
	}
	m.messages[idx].content = content
	if m.messages[idx].dbID != 0 && m.db != nil {
		_ = store.UpdateMessageContent(m.db, m.messages[idx].dbID, content)
	}
	m.reflow()
	m.viewport.GotoBottom()
	return m
}

func (m chatModel) toggleExpandableAt(y int) chatModel {
	if y <= 0 {
		return m
	}
	bodyY := y - 1
	line := m.viewport.YOffset() + bodyY
	for _, hit := range m.rowHits {
		if line < hit.startLine || line >= hit.endLine {
			continue
		}
		if hit.msgIndex < 0 || hit.msgIndex >= len(m.messages) {
			return m
		}
		msg := &m.messages[hit.msgIndex]
		if !msg.isExpandable() || msg.streaming {
			return m
		}
		msg.expanded = !msg.expanded
		m.reflow()
		return m
	}
	return m
}

func (m chatMessage) isExpandable() bool {
	switch m.role {
	case "assistant_reasoning", "tool_call":
		return true
	default:
		return false
	}
}

func (m chatModel) appendStreamingToolCall(requestID int, name, args string) chatModel {
	m.ensureStreamState(requestID)
	entry := toolCallEntry{Name: name, Args: args}
	if !isValidToolName(entry.Name) {
		return m
	}
	m = m.appendMessage(chatMessage{role: "tool_call", content: formatToolCallContent(entry), requestID: requestID})
	m.streamingToolCalls[requestID] = append(m.streamingToolCalls[requestID], len(m.messages)-1)
	return m
}

func (m chatModel) upsertStreamingToolCall(requestID int, entry toolCallEntry) chatModel {
	m.ensureStreamState(requestID)
	if !isValidToolName(entry.Name) {
		return m
	}
	if idx, ok := m.findStreamingToolCallRow(requestID, entry); ok {
		m = m.updateMessageContent(idx, formatToolCallContent(entry))
		return m
	}
	m = m.appendMessage(chatMessage{role: "tool_call", content: formatToolCallContent(entry), requestID: requestID})
	m.streamingToolCalls[requestID] = append(m.streamingToolCalls[requestID], len(m.messages)-1)
	return m
}

func (m chatModel) hasStreamingToolCall(requestID int, name, args string) bool {
	for _, idx := range m.streamingToolCalls[requestID] {
		if idx < 0 || idx >= len(m.messages) {
			continue
		}
		msg := m.messages[idx]
		if msg.requestID != requestID || msg.role != "tool_call" {
			continue
		}
		parsed, ok := parseToolCallContent(msg.content)
		if !ok {
			continue
		}
		if toolCallKey(parsed.Name, parsed.Args) == toolCallKey(name, args) && strings.TrimSpace(parsed.Result) == "" {
			return true
		}
	}
	return false
}

func (m chatModel) findStreamingToolCallRow(requestID int, entry toolCallEntry) (int, bool) {
	for _, idx := range m.streamingToolCalls[requestID] {
		if idx < 0 || idx >= len(m.messages) {
			continue
		}
		msg := m.messages[idx]
		if msg.requestID != requestID || msg.role != "tool_call" {
			continue
		}
		parsed, ok := parseToolCallContent(msg.content)
		if !ok || parsed.Name != entry.Name {
			continue
		}
		if strings.TrimSpace(entry.Result) != "" &&
			toolCallKey(parsed.Name, parsed.Args) == toolCallKey(entry.Name, entry.Args) &&
			strings.TrimSpace(parsed.Result) == "" {
			return idx, true
		}
	}
	return 0, false
}

func (m chatModel) findStreamingToolCallRowIncludingCompleted(requestID int, entry toolCallEntry, used map[int]bool) (int, bool) {
	for _, idx := range m.streamingToolCalls[requestID] {
		if used != nil && used[idx] {
			continue
		}
		if idx < 0 || idx >= len(m.messages) {
			continue
		}
		msg := m.messages[idx]
		if msg.requestID != requestID || msg.role != "tool_call" {
			continue
		}
		parsed, ok := parseToolCallContent(msg.content)
		if !ok {
			continue
		}
		if toolCallKey(parsed.Name, parsed.Args) == toolCallKey(entry.Name, entry.Args) {
			return idx, true
		}
	}
	return 0, false
}

func (m chatModel) startStreamingAssistant(requestID int) chatModel {
	m.ensureStreamState(requestID)
	if _, ok := m.pendingAssistantIdx[requestID]; ok {
		m = m.movePendingAssistantToEnd(requestID)
		return m
	}
	m = m.appendMessage(chatMessage{role: "assistant_pending", streaming: true, requestID: requestID})
	m.pendingAssistantIdx[requestID] = len(m.messages) - 1
	return m
}

func (m chatModel) setStreamingReasoning(requestID int, text string) chatModel {
	return m.appendStreamingSegment(requestID, "assistant_reasoning", text)
}

func (m chatModel) setStreamingAssistant(requestID int, text string) chatModel {
	return m.appendStreamingSegment(requestID, "assistant", text)
}

func (m chatModel) finishStreamingAssistant(requestID int, text string, reasoning string) chatModel {
	m.ensureStreamState(requestID)
	m = m.clearPendingAssistant(requestID)
	if strings.TrimSpace(reasoning) != "" {
		m = m.appendStreamingSegment(requestID, "assistant_reasoning", reasoning)
	}
	if strings.TrimSpace(text) != "" {
		m = m.appendStreamingSegment(requestID, "assistant", text)
	}
	for i := range m.messages {
		if m.messages[i].requestID == requestID {
			m.messages[i].streaming = false
			if m.messages[i].isExpandable() {
				m.messages[i].expanded = false
			}
		}
	}
	delete(m.streamingToolCalls, requestID)
	delete(m.streamStates, requestID)
	delete(m.pendingAssistantIdx, requestID)
	m.reflow()
	m.viewport.GotoBottom()
	return m
}

// persistReasoningForRequest attaches the signed reasoning blocks to the most
// recent persisted assistant message for the given request, so it can be
// replayed on later turns.
func (m chatModel) persistReasoningForRequest(requestID int, model string, blocks []store.ReasoningBlock) chatModel {
	if len(blocks) == 0 || m.db == nil || m.convID == 0 {
		return m
	}
	for i := len(m.messages) - 1; i >= 0; i-- {
		msg := m.messages[i]
		if msg.requestID != requestID || msg.role != "assistant" || msg.dbID == 0 {
			continue
		}
		_ = store.SetMessageReasoning(m.db, msg.dbID, m.convID, model, blocks)
		break
	}
	return m
}

func (m chatModel) appendStreamingSegment(requestID int, role string, cumulative string) chatModel {
	m.ensureStreamState(requestID)
	state := m.streamStates[requestID]
	prev := ""
	switch role {
	case "assistant_reasoning":
		prev = state.lastReasoning
	case "assistant":
		prev = state.lastText
	}
	if cumulative == prev {
		return m
	}
	content := strings.TrimPrefix(cumulative, prev)
	if content == cumulative && prev != "" && strings.HasPrefix(prev, cumulative) {
		return m
	}
	if strings.TrimSpace(content) == "" && strings.TrimSpace(cumulative) == "" {
		return m
	}
	switch role {
	case "assistant_reasoning":
		state.lastReasoning = cumulative
	case "assistant":
		state.lastText = cumulative
	}
	m.streamStates[requestID] = state

	updateIdx := -1
	for i := len(m.messages) - 1; i >= 0; i-- {
		msg := m.messages[i]
		if msg.requestID != requestID {
			continue
		}
		if msg.role == "assistant_pending" {
			continue
		}
		if msg.role == role && msg.streaming {
			updateIdx = i
		}
		break
	}
	if updateIdx >= 0 {
		if content == cumulative && prev == "" {
			m.messages[updateIdx].content = cumulative
		} else {
			m.messages[updateIdx].content += content
		}
		if m.messages[updateIdx].dbID != 0 && m.db != nil {
			_ = store.UpdateMessageContent(m.db, m.messages[updateIdx].dbID, m.messages[updateIdx].content)
		}
		m = m.movePendingAssistantToEnd(requestID)
		m.reflow()
		m.viewport.GotoBottom()
		return m
	}
	if content == "" {
		content = cumulative
	}
	m = m.appendMessage(chatMessage{role: role, content: content, streaming: true, requestID: requestID, expanded: true})
	return m.movePendingAssistantToEnd(requestID)
}

func (m chatModel) clearPendingAssistant(requestID int) chatModel {
	idx, ok := m.pendingAssistantIdx[requestID]
	if !ok {
		return m
	}
	delete(m.pendingAssistantIdx, requestID)
	if idx < 0 || idx >= len(m.messages) {
		return m
	}
	m.messages = append(m.messages[:idx], m.messages[idx+1:]...)
	for id, cur := range m.pendingAssistantIdx {
		if cur > idx {
			m.pendingAssistantIdx[id] = cur - 1
		}
	}
	for reqID, toolMap := range m.streamingToolCalls {
		for key, cur := range toolMap {
			if cur > idx {
				toolMap[key] = cur - 1
			}
		}
		m.streamingToolCalls[reqID] = toolMap
	}
	m.reflow()
	m.viewport.GotoBottom()
	return m
}

func (m chatModel) movePendingAssistantToEnd(requestID int) chatModel {
	idx, ok := m.pendingAssistantIdx[requestID]
	if !ok || idx < 0 || idx >= len(m.messages) || idx == len(m.messages)-1 {
		return m
	}
	msg := m.messages[idx]
	m.messages = append(m.messages[:idx], m.messages[idx+1:]...)
	m.messages = append(m.messages, msg)
	for id, cur := range m.pendingAssistantIdx {
		switch {
		case id == requestID:
			m.pendingAssistantIdx[id] = len(m.messages) - 1
		case cur > idx:
			m.pendingAssistantIdx[id] = cur - 1
		}
	}
	for reqID, toolMap := range m.streamingToolCalls {
		for key, cur := range toolMap {
			if cur > idx {
				toolMap[key] = cur - 1
			}
		}
		m.streamingToolCalls[reqID] = toolMap
	}
	m.reflow()
	m.viewport.GotoBottom()
	return m
}

// formatMessage renders a single chat message as a full-width left-border strip.
// frame drives the streaming dot animation; pass 0 when not animating.
func formatMessage(msg chatMessage, width int, frame int, term TerminalProfile) string {
	if width <= 0 {
		width = 80
	}

	switch msg.role {
	case "user":
		label := styleSenderUser.Render("you ›")
		bodyW := max(16, width-styleUserMsg.GetHorizontalFrameSize())
		body := lipgloss.Wrap(msg.content, bodyW, " ")
		return styleUserMsg.Width(width).Render(label + "\n" + body)

	case "assistant_pending":
		dotLevels := []lipgloss.Style{
			lipgloss.NewStyle().Foreground(colorDim),
			lipgloss.NewStyle().Foreground(colorMuted),
			lipgloss.NewStyle().Foreground(colorAmber),
			lipgloss.NewStyle().Foreground(colorMuted),
		}
		d := func(offset int) string { return dotLevels[(frame+offset)%4].Render("●") }
		return styleAgentMsg.Width(width).Render(d(0) + " " + d(1) + " " + d(2))

	case "assistant":
		label := styleSenderBot.Render("◆ Tether")
		bodyW := max(16, width-styleAgentMsg.GetHorizontalFrameSize())
		body := strings.TrimSpace(msg.content)
		if msg.streaming && body == "" {
			// Animated dot pulse: four brightness levels, each dot offset by 1 frame.
			dotLevels := []lipgloss.Style{
				lipgloss.NewStyle().Foreground(colorDim),
				lipgloss.NewStyle().Foreground(colorMuted),
				lipgloss.NewStyle().Foreground(colorAmber),
				lipgloss.NewStyle().Foreground(colorMuted),
			}
			d := func(offset int) string { return dotLevels[(frame+offset)%4].Render("●") }
			body = d(0) + " " + d(1) + " " + d(2)
		} else if body != "" {
			body = renderAssistantBody(body, bodyW, term)
		}
		return styleAgentMsg.Width(width).Render(label + "\n" + body)

	case "assistant_reasoning":
		label := styleReasoningHeader.Render("◈ reasoning")
		bodyW := max(16, width-styleToolResult.GetHorizontalFrameSize())
		body := lipgloss.Wrap(strings.TrimSpace(msg.content), bodyW, " ")
		if msg.streaming && strings.TrimSpace(body) == "" {
			body = styleReasoningHint.Render("thinking…")
		}
		if !msg.streaming && !msg.expanded {
			return styleToolResult.Width(width).Render(label + "  " + styleReasoningHint.Render("click to expand"))
		}
		return styleToolResult.Width(width).Render(label + "\n" + body)

	case "tool_call":
		if !isValidToolCallContent(msg.content) {
			senderLabel, s := systemMessageVariant(msg.content)
			bodyW := max(16, width-s.GetHorizontalFrameSize())
			rendered := renderRichText(msg.content, bodyW, richTextSystem)
			return s.Width(width).Render(styleSenderSystem.Render(senderLabel) + "\n" + rendered)
		}
		entry, _ := parseToolCallContent(msg.content)
		invLine := styleAccent.Render("▷") + "  " + styleTitle.Render(entry.Name)
		if args := strings.TrimSpace(entry.Args); args != "" {
			invLine += "  " + styleMuted.Render("·") + "  " + args
		}
		invRow := styleToolStrip.Width(width).Render(invLine)
		if result := strings.TrimSpace(entry.Result); result != "" {
			if !msg.streaming && !msg.expanded {
				return styleToolStrip.Width(width).Render(styleInfo.Render("✓") + "  " + styleTitle.Render(entry.Name) + "  " + styleReasoningHint.Render("click to expand"))
			}
			return invRow + "\n" + styleToolResult.Width(width).Render("✓  "+result)
		}
		return invRow + "\n" + styleToolResult.Width(width).Render("·  running…")

	default: // system
		senderLabel, s := systemMessageVariant(msg.content)
		bodyW := max(16, width-s.GetHorizontalFrameSize())
		rendered := renderRichText(msg.content, bodyW, richTextSystem)
		return s.Width(width).Render(styleSenderSystem.Render(senderLabel) + "\n" + rendered)
	}
}

func renderAssistantBody(text string, width int, term TerminalProfile) string {
	if looksLikeRichText(text) {
		return renderRichText(text, width, richTextAssistant)
	}
	wrapped := lipgloss.Wrap(text, width, " ")
	return wrapped
}

func looksLikeRichText(text string) bool {
	text = strings.TrimSpace(text)
	if text == "" {
		return false
	}
	richMarkers := []string{"```", "#", "*", "_", "[", "](", "|", "\n- ", "\n1. ", "\n## ", "\n### ", "\n---"}
	for _, marker := range richMarkers {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

// systemMessageVariant picks a sender label and style based on the content prefix.
func systemMessageVariant(content string) (string, lipgloss.Style) {
	c := strings.ToLower(strings.TrimSpace(content))
	errorPrefixes := []string{
		"(agent error)", "failed", "invalid", "unknown command",
		"secrets unavailable", "error:", "✗",
	}
	for _, p := range errorPrefixes {
		if strings.HasPrefix(c, p) {
			return "✗ error", styleErrorMsg
		}
	}
	successPrefixes := []string{
		"signal linked", "discord linked", "discord unlinked", "signal unlinked",
		"memory added", "memory updated", "memory deleted",
		"task added", "task updated", "task marked done",
		"secret stored", "deleted secret", "cleared all secrets",
		"updated role", "started a fresh", "spawned subagent",
	}
	for _, p := range successPrefixes {
		if strings.HasPrefix(c, p) {
			return "✓ info", styleInfoMsg
		}
	}
	return "● system", styleSystemMsg
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

func loadChatMessages(db *sql.DB, convID int64, limit int) ([]chatMessage, error) {
	msgs, err := store.ListRecentMessages(db, convID, limit)
	if err != nil {
		return nil, err
	}
	chatMsgs := make([]chatMessage, 0, len(msgs))
	for _, mm := range msgs {
		role := mm.Role
		if mm.IsNotice {
			role = "system"
		}
		if role == "tool_call" && !isValidToolCallContent(mm.Content) {
			role = "system"
		}
		chatMsgs = append(chatMsgs, chatMessage{
			role:     role,
			content:  mm.Content,
			dbID:     mm.ID,
			expanded: !(role == "assistant_reasoning" || role == "tool_call"),
		})
	}
	return chatMsgs, nil
}

func preserveLoadedMessageState(prev, loaded []chatMessage) []chatMessage {
	if len(prev) == 0 || len(loaded) == 0 {
		return loaded
	}
	expandedByID := make(map[int64]bool, len(prev))
	for _, msg := range prev {
		if msg.dbID == 0 || !msg.isExpandable() {
			continue
		}
		expandedByID[msg.dbID] = msg.expanded
	}
	for i := range loaded {
		if loaded[i].dbID == 0 || !loaded[i].isExpandable() {
			continue
		}
		if expanded, ok := expandedByID[loaded[i].dbID]; ok {
			loaded[i].expanded = expanded
		}
	}
	return loaded
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
		active := m.focus == composerFocusSuggestion && i == m.selectedSuggestion
		line := s.Label
		if strings.TrimSpace(s.Detail) != "" {
			detailStyle := styleAutocompleteDetail
			if active {
				detailStyle = styleAutocompleteDetailActive
			}
			line += "  " + detailStyle.Render(s.Detail)
		}
		if active {
			rows = append(rows, styleAutocompleteSuggestionActive.Width(width).Render(line))
		} else {
			rows = append(rows, styleAutocompleteSuggestion.Width(width).Render(line))
		}
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
	prompt := styleChatPrompt.Render("❯")
	promptW := lipgloss.Width(prompt + " ")
	inputInnerW := max(10, composerW-promptW-styleChatInputBox.GetHorizontalFrameSize())
	inputBox := styleChatInputBox.Width(inputInnerW).Render(m.textarea.View())
	inputRow := prompt + " " + inputBox
	hintsLine := styleChatHint.Render(strings.Join([]string{
		styleChatHintKey.Render("tab") + " cycle",
		styleChatHintKey.Render("↵") + " apply",
		styleChatHintKey.Render("esc") + " dismiss",
	}, "  "))
	emptyLine := styleChatRow.Width(composerW).Render("")
	body := emptyLine + "\n" + inputRow + "\n" + hintsLine
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
		{Label: "/status", InsertValue: "/status", Detail: "show current session metrics"},
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
