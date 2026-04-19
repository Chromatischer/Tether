package tui

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"tether/internal/store"
)

type memoryModel struct {
	db     *sql.DB
	userID int64

	viewport viewport.Model
	items    []store.MemoryItem // flat, all kinds, sorted pinned-first per kind
	cursor   int               // index into items

	// inline edit mode
	editing   bool
	editInput textinput.Model

	// expiry prompt mode
	expiryMode  bool
	expiryInput textinput.Model

	status    string
	statusErr bool

	w, h int
}

type memoryLoadedMsg struct {
	Items []store.MemoryItem
	Err   error
}

func newMemoryModel() memoryModel {
	vp := viewport.New(viewport.WithWidth(80), viewport.WithHeight(10))
	vp.KeyMap.Left.SetEnabled(false)
	vp.KeyMap.Right.SetEnabled(false)

	ei := textinput.New()
	ei.Prompt = "> "
	ei.CharLimit = 500

	xi := textinput.New()
	xi.Prompt = "expire in days (0=clear): "
	xi.CharLimit = 5

	return memoryModel{viewport: vp, editInput: ei, expiryInput: xi}
}

func (m memoryModel) withUser(db *sql.DB, userID int64) memoryModel {
	m.db = db
	m.userID = userID
	return m
}

func (m memoryModel) withSize(w, h int) memoryModel {
	if w <= 0 || h <= 0 {
		return m
	}
	m.w, m.h = w, h
	m.viewport.SetWidth(w)
	m.viewport.SetHeight(h)
	m.editInput.SetWidth(max(20, w-4))
	m.expiryInput.SetWidth(max(20, w-4))
	m.rebuild()
	return m
}

func (m memoryModel) loadCmd() tea.Cmd {
	db := m.db
	userID := m.userID
	return func() tea.Msg {
		if db == nil || userID == 0 {
			return memoryLoadedMsg{}
		}
		items, err := store.ListMemoryItems(db, userID, "", 200)
		if err != nil {
			return memoryLoadedMsg{Err: err}
		}
		return memoryLoadedMsg{Items: items}
	}
}

func (m memoryModel) Update(msg tea.Msg) (memoryModel, tea.Cmd) {
	switch msg := msg.(type) {
	case memoryLoadedMsg:
		if msg.Err != nil {
			m.status = "error: " + msg.Err.Error()
			m.statusErr = true
		} else {
			m.items = msg.Items
			if m.cursor >= len(m.items) {
				m.cursor = max(0, len(m.items)-1)
			}
			m.status = ""
			m.statusErr = false
		}
		m.rebuild()
		return m, nil

	case tea.KeyPressMsg:
		// Expiry input mode
		if m.expiryMode {
			switch msg.String() {
			case "enter":
				m.expiryMode = false
				if m.cursor < len(m.items) {
					item := m.items[m.cursor]
					days, err := strconv.Atoi(strings.TrimSpace(m.expiryInput.Value()))
					if err != nil {
						m.status = "invalid number"
						m.statusErr = true
						m.rebuild()
						return m, nil
					}
					var t *time.Time
					if days > 0 {
						exp := time.Now().Add(time.Duration(days) * 24 * time.Hour)
						t = &exp
					}
					if err := store.SetMemoryExpiry(m.db, m.userID, item.ID, t); err != nil {
						m.status = "error: " + err.Error()
						m.statusErr = true
						m.rebuild()
						return m, nil
					}
					m.status = "expiry set"
					m.statusErr = false
				}
				m.rebuild()
				return m, m.loadCmd()
			case "esc":
				m.expiryMode = false
				m.rebuild()
				return m, nil
			}
			var cmd tea.Cmd
			m.expiryInput, cmd = m.expiryInput.Update(msg)
			m.rebuild()
			return m, cmd
		}

		// Edit mode
		if m.editing {
			switch msg.String() {
			case "enter":
				m.editing = false
				if m.cursor < len(m.items) {
					item := m.items[m.cursor]
					if err := store.UpdateMemoryItem(m.db, m.userID, item.ID, m.editInput.Value()); err != nil {
						m.status = "error: " + err.Error()
						m.statusErr = true
						m.rebuild()
						return m, nil
					}
					m.status = "saved"
					m.statusErr = false
				}
				m.rebuild()
				return m, m.loadCmd()
			case "esc":
				m.editing = false
				m.rebuild()
				return m, nil
			}
			var cmd tea.Cmd
			m.editInput, cmd = m.editInput.Update(msg)
			m.rebuild()
			return m, cmd
		}

		// Normal navigation
		switch msg.String() {
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
				m.rebuild()
			}
		case "down", "j":
			if m.cursor < len(m.items)-1 {
				m.cursor++
				m.rebuild()
			}
		case "e":
			if m.cursor < len(m.items) {
				m.editing = true
				m.editInput.SetValue(m.items[m.cursor].Content)
				m.editInput.Focus()
				m.editInput.CursorEnd()
				m.rebuild()
			}
		case "p":
			if m.cursor < len(m.items) {
				item := m.items[m.cursor]
				if err := store.SetMemoryPin(m.db, m.userID, item.ID, !item.Pinned); err != nil {
					m.status = "error: " + err.Error()
					m.statusErr = true
					m.rebuild()
					return m, nil
				}
				m.status = ""
				return m, m.loadCmd()
			}
		case "x":
			if m.cursor < len(m.items) {
				m.expiryMode = true
				m.expiryInput.SetValue("")
				m.expiryInput.Focus()
				m.rebuild()
			}
		case "D":
			if m.cursor < len(m.items) {
				item := m.items[m.cursor]
				if err := store.DeleteMemoryItem(m.db, m.userID, item.ID); err != nil {
					m.status = "error: " + err.Error()
					m.statusErr = true
					m.rebuild()
					return m, nil
				}
				m.items = append(m.items[:m.cursor], m.items[m.cursor+1:]...)
				if m.cursor >= len(m.items) {
					m.cursor = max(0, len(m.items)-1)
				}
				m.status = ""
				m.rebuild()
			}
		case "r":
			return m, m.loadCmd()
		}
	}

	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	return m, cmd
}

// rebuild regenerates the viewport content from m.items and cursor state.
func (m *memoryModel) rebuild() {
	if m.viewport.Width() <= 0 {
		return
	}

	var b strings.Builder
	// lineToItemIdx maps rendered line number -> index in m.items
	lineToItemIdx := map[int]int{}
	lineNum := 0

	kinds := []string{"task", "fact", "pref"}
	kindLabels := map[string]string{"task": "tasks", "fact": "facts", "pref": "preferences"}

	for _, kind := range kinds {
		// Collect indices for this kind
		var kindIdxs []int
		for i, it := range m.items {
			if it.Kind == kind {
				kindIdxs = append(kindIdxs, i)
			}
		}
		if len(kindIdxs) == 0 {
			continue
		}

		// Section header
		b.WriteString(styleAccent.Render(kindLabels[kind]) + "  " + styleDim.Render("─────────────────────") + "\n")
		lineNum++

		for _, idx := range kindIdxs {
			it := m.items[idx]
			lineToItemIdx[lineNum] = idx

			// Prefix indicator
			prefix := "  "
			if it.Pinned {
				prefix = styleSenderBot.Render("●") + " "
			} else if it.LastUsedAt != nil && time.Since(*it.LastUsedAt) < time.Hour {
				prefix = styleAccent.Render("→") + " "
			}

			// Suffix indicator
			suffix := ""
			if it.ExpiresAt != nil {
				days := int(time.Until(*it.ExpiresAt).Hours() / 24)
				suffix = "  " + styleDim.Render(fmt.Sprintf("(exp %dd)", days))
			}

			// Content (replaced by input in edit mode)
			if m.editing && idx == m.cursor {
				b.WriteString(prefix + m.editInput.View() + suffix + "\n")
			} else if idx == m.cursor {
				contentW := m.viewport.Width() - lipgloss.Width(prefix) - lipgloss.Width(suffix)
				if contentW < 0 {
					contentW = 0
				}
				highlighted := styleTitle.Width(contentW).Render(it.Content)
				b.WriteString(prefix + highlighted + suffix + "\n")
			} else {
				b.WriteString(prefix + styleMuted.Render(it.Content) + suffix + "\n")
			}
			lineNum++
		}
		b.WriteString("\n")
		lineNum++
	}

	if m.expiryMode {
		b.WriteString(m.expiryInput.View() + "\n")
		lineNum++
	}

	// Hint / status line
	hint := styleDim.Render("↑↓·move  e·edit  p·pin  x·expire  D·delete  r·refresh")
	if m.status != "" {
		if m.statusErr {
			hint = styleError.Render(m.status)
		} else {
			hint = styleInfo.Render(m.status) + "  " + styleDim.Render("r·refresh")
		}
	}
	content := strings.TrimRight(b.String(), "\n") + "\n\n" + hint
	m.viewport.SetContent(content)

	// Scroll to keep cursor visible
	for line, idx := range lineToItemIdx {
		if idx == m.cursor {
			offset := line - m.viewport.Height()/2
			if offset < 0 {
				offset = 0
			}
			m.viewport.SetYOffset(offset)
			break
		}
	}
}

func (m memoryModel) View() tea.View {
	content := m.viewport.View()
	if m.w > 0 {
		content = lipgloss.NewStyle().Background(colorBg).Width(m.w).Render(content)
	}
	return tea.NewView(content)
}
