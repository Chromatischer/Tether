package tui

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"tether/internal/store"
)

type adminTab int

const (
	adminTabAudit adminTab = iota
	adminTabUsers
	adminTabJobs
	adminTabSignal
)

var adminTabLabels = []string{"audit", "users", "jobs", "signal"}

type adminModel struct {
	ctx  *SessionContext
	tab  adminTab
	w, h int

	audit  viewport.Model
	users  viewport.Model
	jobs   viewport.Model
	signal viewport.Model

	userList []store.User
	userSel  int
}

type adminLoadMsg struct {
	tab     adminTab
	content string
	users   []store.User
	err     error
}

func newAdminModel(ctx *SessionContext) adminModel {
	mk := func() viewport.Model {
		vp := viewport.New(viewport.WithWidth(80), viewport.WithHeight(10))
		vp.KeyMap.Left.SetEnabled(false)
		vp.KeyMap.Right.SetEnabled(false)
		return vp
	}
	return adminModel{
		ctx:    ctx,
		audit:  mk(),
		users:  mk(),
		jobs:   mk(),
		signal: mk(),
	}
}

func (m adminModel) withSize(w, h int) adminModel {
	if w <= 0 || h <= 0 {
		return m
	}
	m.w, m.h = w, h
	ch := max(1, h-1) // 1 line for sub-tab bar
	m.audit.SetWidth(w)
	m.audit.SetHeight(ch)
	m.users.SetWidth(w)
	m.users.SetHeight(ch)
	m.jobs.SetWidth(w)
	m.jobs.SetHeight(ch)
	m.signal.SetWidth(w)
	m.signal.SetHeight(ch)
	return m
}

func (m adminModel) loadCmd() tea.Cmd {
	return m.loadTabCmd(m.tab)
}

func (m adminModel) loadTabCmd(tab adminTab) tea.Cmd {
	ctx := m.ctx
	return func() tea.Msg {
		switch tab {

		case adminTabAudit:
			evs, err := store.ListAuditEvents(ctx.DB, 100)
			if err != nil {
				return adminLoadMsg{tab: tab, err: err}
			}
			var b strings.Builder
			for _, ev := range evs {
				ts := ev.CreatedAt.UTC().Format("01-02 15:04:05")
				line := styleDim.Render(ts) + "  " + styleAccent.Render(ev.Type)
				if ev.UserID != nil {
					line += styleDim.Render(fmt.Sprintf("  user=%d", *ev.UserID))
				}
				if ev.Payload != "" {
					line += "  " + ev.Payload
				}
				b.WriteString(line + "\n")
			}
			return adminLoadMsg{tab: tab, content: strings.TrimRight(b.String(), "\n")}

		case adminTabUsers:
			users, err := store.ListUsers(ctx.DB)
			if err != nil {
				return adminLoadMsg{tab: tab, err: err}
			}
			return adminLoadMsg{tab: tab, users: users}

		case adminTabJobs:
			lastTick, ok, err := store.LatestAuditEventTime(ctx.DB, "proactive_tick")
			lt := "(never)"
			if err == nil && ok {
				lt = lastTick.UTC().Format(time.RFC3339)
			}
			undelivered, _ := store.CountUndeliveredNotifications(ctx.DB)
			content := styleTitle.Render("jobs") + "\n\n" +
				styleMuted.Render("proactive_tick last run") + "\n  " + lt + "\n\n" +
				styleMuted.Render("undelivered notifications") + "\n  " + fmt.Sprintf("%d", undelivered) + "\n\n" +
				styleDim.Render("r · refresh")
			return adminLoadMsg{tab: tab, content: content}

		case adminTabSignal:
			if !ctx.Config.Signal.Enabled {
				return adminLoadMsg{tab: tab, content: styleTitle.Render("signal") + "\n\n" + styleDim.Render("disabled in config")}
			}
			addr := ctx.Config.Signal.HTTPAddr
			base := addr
			if !strings.HasPrefix(base, "http") {
				base = "http://" + base
			}
			checkLine := styleDim.Render("(checking…)")
			if req, err := http.NewRequest(http.MethodGet, base+"/api/v1/check", nil); err == nil {
				if resp, err := http.DefaultClient.Do(req); err != nil {
					checkLine = styleError.Render("check failed: " + err.Error())
				} else {
					resp.Body.Close()
					checkLine = styleInfo.Render(fmt.Sprintf("HTTP %d", resp.StatusCode))
				}
			}
			content := styleTitle.Render("signal") + "\n\n" +
				styleMuted.Render("account") + "\n  " + ctx.Config.Signal.AccountNumber + "\n\n" +
				styleMuted.Render("http addr") + "\n  " + addr + "\n\n" +
				styleMuted.Render("status") + "\n  " + checkLine + "\n\n" +
				styleDim.Render("r · refresh")
			return adminLoadMsg{tab: tab, content: content}
		}
		return nil
	}
}

func (m adminModel) Update(msg tea.Msg) (adminModel, tea.Cmd) {
	switch msg := msg.(type) {
	case adminLoadMsg:
		if msg.err != nil {
			s := styleError.Render("error: " + msg.err.Error())
			m.setTabContent(msg.tab, s)
			return m, nil
		}
		switch msg.tab {
		case adminTabUsers:
			m.userList = msg.users
			m.userSel = 0
			m.rebuildUsersViewport()
		default:
			m.setTabContent(msg.tab, msg.content)
		}
		return m, nil

	case tea.KeyPressMsg:
		switch msg.String() {
		case "1":
			return m.switchTab(adminTabAudit)
		case "2":
			return m.switchTab(adminTabUsers)
		case "3":
			return m.switchTab(adminTabJobs)
		case "4":
			return m.switchTab(adminTabSignal)
		case "[":
			next := (int(m.tab) - 1 + len(adminTabLabels)) % len(adminTabLabels)
			return m.switchTab(adminTab(next))
		case "]":
			next := (int(m.tab) + 1) % len(adminTabLabels)
			return m.switchTab(adminTab(next))
		case "r":
			return m, m.loadTabCmd(m.tab)
		case "up":
			if m.tab == adminTabUsers && m.userSel > 0 {
				m.userSel--
				m.rebuildUsersViewport()
				return m, nil
			}
		case "down":
			if m.tab == adminTabUsers && m.userSel < len(m.userList)-1 {
				m.userSel++
				m.rebuildUsersViewport()
				return m, nil
			}
		case "p":
			if m.tab == adminTabUsers && m.userSel < len(m.userList) {
				u := m.userList[m.userSel]
				_ = store.SetUserRole(m.ctx.DB, u.Username, "admin")
				return m, m.loadTabCmd(adminTabUsers)
			}
		case "d":
			if m.tab == adminTabUsers && m.userSel < len(m.userList) {
				u := m.userList[m.userSel]
				_ = store.SetUserRole(m.ctx.DB, u.Username, "user")
				return m, m.loadTabCmd(adminTabUsers)
			}
		}
	}

	var cmd tea.Cmd
	switch m.tab {
	case adminTabAudit:
		m.audit, cmd = m.audit.Update(msg)
	case adminTabUsers:
		m.users, cmd = m.users.Update(msg)
	case adminTabJobs:
		m.jobs, cmd = m.jobs.Update(msg)
	case adminTabSignal:
		m.signal, cmd = m.signal.Update(msg)
	}
	return m, cmd
}

func (m adminModel) switchTab(tab adminTab) (adminModel, tea.Cmd) {
	m.tab = tab
	return m, m.loadTabCmd(tab)
}

func (m *adminModel) setTabContent(tab adminTab, content string) {
	switch tab {
	case adminTabAudit:
		m.audit.SetContent(content)
		m.audit.GotoTop()
	case adminTabUsers:
		m.users.SetContent(content)
		m.users.GotoTop()
	case adminTabJobs:
		m.jobs.SetContent(content)
		m.jobs.GotoTop()
	case adminTabSignal:
		m.signal.SetContent(content)
		m.signal.GotoTop()
	}
}

func (m *adminModel) rebuildUsersViewport() {
	var b strings.Builder
	b.WriteString(styleTitle.Render("users") + "\n\n")
	for i, u := range m.userList {
		roleTag := styleDim.Render("[" + u.Role + "]")
		var line string
		if i == m.userSel {
			line = styleTabActive.Render(" "+u.Username+" ") + "  " + roleTag
		} else {
			line = "  " + u.Username + "  " + roleTag
		}
		b.WriteString(line + "\n")
	}
	b.WriteString("\n" + styleDim.Render("↑↓ · select   p · promote   d · demote   r · refresh"))
	m.users.SetContent(strings.TrimRight(b.String(), "\n"))
}

func (m adminModel) View() tea.View {
	tabs := make([]string, len(adminTabLabels))
	for i, label := range adminTabLabels {
		if adminTab(i) == m.tab {
			tabs[i] = styleTabActive.Render(label)
		} else {
			tabs[i] = styleTab.Render(label)
		}
	}
	tabBar := lipgloss.JoinHorizontal(lipgloss.Top, tabs...)

	var body string
	switch m.tab {
	case adminTabAudit:
		body = m.audit.View()
	case adminTabUsers:
		body = m.users.View()
	case adminTabJobs:
		body = m.jobs.View()
	case adminTabSignal:
		body = m.signal.View()
	}
	return tea.NewView(tabBar + "\n" + body)
}
