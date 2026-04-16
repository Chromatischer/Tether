# Admin / Memory / Settings TUI — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add three fully-functional TUI views to the Tether SSH app: an admin/audit panel with sub-tabs, a cursor-driven memory manager with pin/edit/expiry, and a settings panel covering proactive rules, confirmation strictness, Signal, and retention.

**Architecture:** Each view is a self-contained `*Model` in its own file (`admin.go`, enhanced `memory.go`, `settings.go`). `app.go` stays thin as a router. Two new DB migrations add columns to `memory_items` and create `user_settings`. Store functions and the agent runtime are updated before the TUI is wired.

**Tech Stack:** Go 1.25, charm.land/bubbletea/v2, charm.land/lipgloss/v2, charm.land/bubbles/v2 (viewport, textinput), SQLite via `modernc.org/sqlite`.

---

## File Map

| Action | Path | Responsibility |
|--------|------|----------------|
| Create | `internal/db/migrations/0010_memory_enhancements.sql` | Add pinned/expires_at/last_used_at to memory_items |
| Create | `internal/db/migrations/0011_user_settings.sql` | Create user_settings key-value table |
| Modify | `internal/store/memory.go` | Add UpdateMemoryItem, SetMemoryPin, SetMemoryExpiry, TouchMemoryItems; update ListMemoryItems |
| Create | `internal/store/settings.go` | GetUserSetting, SetUserSetting, GetAllUserSettings |
| Create | `internal/tui/admin.go` | adminModel with 4 sub-tabs |
| Modify | `internal/tui/memory.go` | Replace read-only viewport with cursor-driven CRUD list |
| Create | `internal/tui/settings.go` | settingsModel with 4 sections |
| Modify | `internal/tui/app.go` | Add viewAdmin, wire admin/settings, update headerButtons/Update/View |
| Modify | `internal/agent/context.go` | Call TouchMemoryItems after injecting memory into prompt |

---

## Task 1: DB Migrations

**Files:**
- Create: `internal/db/migrations/0010_memory_enhancements.sql`
- Create: `internal/db/migrations/0011_user_settings.sql`

- [ ] **Step 1: Write 0010_memory_enhancements.sql**

```sql
ALTER TABLE memory_items ADD COLUMN pinned       INTEGER NOT NULL DEFAULT 0;
ALTER TABLE memory_items ADD COLUMN expires_at   INTEGER;
ALTER TABLE memory_items ADD COLUMN last_used_at INTEGER;
```

- [ ] **Step 2: Write 0011_user_settings.sql**

```sql
CREATE TABLE IF NOT EXISTS user_settings (
    user_id INTEGER NOT NULL,
    key     TEXT    NOT NULL,
    value   TEXT    NOT NULL,
    PRIMARY KEY (user_id, key)
);
```

- [ ] **Step 3: Build to verify embed compiles**

```bash
go build ./internal/db/...
```

Expected: no output (success).

- [ ] **Step 4: Commit**

```bash
git add internal/db/migrations/0010_memory_enhancements.sql internal/db/migrations/0011_user_settings.sql
git commit -m "feat: add memory enhancements and user_settings migrations"
```

---

## Task 2: Store — Memory Enhancements

**Files:**
- Modify: `internal/store/memory.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/store/memory_test.go`:

```go
package store_test

import (
	"database/sql"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"tether/internal/db"
	"tether/internal/store"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	d, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(d); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func TestUpdateMemoryItem(t *testing.T) {
	d := openTestDB(t)
	id, err := store.AddMemoryItem(d, 1, "fact", "original")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateMemoryItem(d, 1, id, "updated"); err != nil {
		t.Fatal(err)
	}
	items, err := store.ListMemoryItems(d, 1, "fact", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Content != "updated" {
		t.Fatalf("expected updated content, got %v", items)
	}
}

func TestSetMemoryPin(t *testing.T) {
	d := openTestDB(t)
	id, _ := store.AddMemoryItem(d, 1, "fact", "pinnable")
	if err := store.SetMemoryPin(d, 1, id, true); err != nil {
		t.Fatal(err)
	}
	items, _ := store.ListMemoryItems(d, 1, "fact", 10)
	if !items[0].Pinned {
		t.Fatal("expected pinned=true")
	}
}

func TestSetMemoryExpiry(t *testing.T) {
	d := openTestDB(t)
	id, _ := store.AddMemoryItem(d, 1, "fact", "expirable")
	past := time.Now().Add(-time.Hour)
	if err := store.SetMemoryExpiry(d, 1, id, &past); err != nil {
		t.Fatal(err)
	}
	// Expired item should not appear
	items, _ := store.ListMemoryItems(d, 1, "fact", 10)
	if len(items) != 0 {
		t.Fatal("expected expired item to be filtered out")
	}
}

func TestTouchMemoryItems(t *testing.T) {
	d := openTestDB(t)
	id, _ := store.AddMemoryItem(d, 1, "fact", "touchable")
	if err := store.TouchMemoryItems(d, []int64{id}); err != nil {
		t.Fatal(err)
	}
	items, _ := store.ListMemoryItems(d, 1, "fact", 10)
	if items[0].LastUsedAt == nil {
		t.Fatal("expected last_used_at to be set")
	}
}

func TestListMemoryItemsPinnedFirst(t *testing.T) {
	d := openTestDB(t)
	id1, _ := store.AddMemoryItem(d, 1, "fact", "regular")
	id2, _ := store.AddMemoryItem(d, 1, "fact", "pinned")
	_ = store.SetMemoryPin(d, 1, id2, true)
	items, _ := store.ListMemoryItems(d, 1, "fact", 10)
	if items[0].ID != id2 {
		t.Fatalf("expected pinned item first, got id=%d (id1=%d id2=%d)", items[0].ID, id1, id2)
	}
}
```

- [ ] **Step 2: Run tests — expect failure**

```bash
go test ./internal/store/... -run "TestUpdateMemoryItem|TestSetMemoryPin|TestSetMemoryExpiry|TestTouchMemoryItems|TestListMemoryItemsPinnedFirst" -v 2>&1 | head -30
```

Expected: compile error — `store.UpdateMemoryItem undefined` etc.

- [ ] **Step 3: Update `internal/store/memory.go`**

Replace the entire file with:

```go
package store

import (
	"database/sql"
	"time"
)

type MemoryItem struct {
	ID         int64
	UserID     int64
	Kind       string
	Content    string
	Pinned     bool
	ExpiresAt  *time.Time
	LastUsedAt *time.Time
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

func AddMemoryItem(db *sql.DB, userID int64, kind, content string) (int64, error) {
	now := time.Now().Unix()
	res, err := db.Exec(
		`INSERT INTO memory_items(user_id, kind, content, created_at, updated_at) VALUES (?,?,?,?,?)`,
		userID, kind, content, now, now,
	)
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	return id, nil
}

// ListMemoryItems returns items for a user, filtered by kind (empty = all),
// excluding expired items, sorted pinned-first then newest-first.
func ListMemoryItems(db *sql.DB, userID int64, kind string, limit int) ([]MemoryItem, error) {
	if limit <= 0 {
		limit = 100
	}
	now := time.Now().Unix()
	rows, err := db.Query(`
		SELECT id, user_id, kind, content, pinned,
		       expires_at, last_used_at, created_at, updated_at
		FROM memory_items
		WHERE user_id=?
		  AND (?='' OR kind=?)
		  AND (expires_at IS NULL OR expires_at > ?)
		ORDER BY pinned DESC, id DESC
		LIMIT ?`,
		userID, kind, kind, now, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MemoryItem{}
	for rows.Next() {
		var it MemoryItem
		var pinned int
		var expiresAt, lastUsedAt sql.NullInt64
		var cAt, uAt int64
		if err := rows.Scan(
			&it.ID, &it.UserID, &it.Kind, &it.Content, &pinned,
			&expiresAt, &lastUsedAt, &cAt, &uAt,
		); err != nil {
			return nil, err
		}
		it.Pinned = pinned != 0
		if expiresAt.Valid {
			t := time.Unix(expiresAt.Int64, 0)
			it.ExpiresAt = &t
		}
		if lastUsedAt.Valid {
			t := time.Unix(lastUsedAt.Int64, 0)
			it.LastUsedAt = &t
		}
		it.CreatedAt = time.Unix(cAt, 0)
		it.UpdatedAt = time.Unix(uAt, 0)
		out = append(out, it)
	}
	return out, rows.Err()
}

func DeleteMemoryItem(db *sql.DB, userID, id int64) error {
	_, err := db.Exec(`DELETE FROM memory_items WHERE user_id=? AND id=?`, userID, id)
	return err
}

func UpdateMemoryItem(db *sql.DB, userID, id int64, content string) error {
	now := time.Now().Unix()
	_, err := db.Exec(
		`UPDATE memory_items SET content=?, updated_at=? WHERE user_id=? AND id=?`,
		content, now, userID, id,
	)
	return err
}

func SetMemoryPin(db *sql.DB, userID, id int64, pinned bool) error {
	v := 0
	if pinned {
		v = 1
	}
	_, err := db.Exec(
		`UPDATE memory_items SET pinned=? WHERE user_id=? AND id=?`,
		v, userID, id,
	)
	return err
}

func SetMemoryExpiry(db *sql.DB, userID, id int64, t *time.Time) error {
	if t == nil {
		_, err := db.Exec(
			`UPDATE memory_items SET expires_at=NULL WHERE user_id=? AND id=?`,
			userID, id,
		)
		return err
	}
	_, err := db.Exec(
		`UPDATE memory_items SET expires_at=? WHERE user_id=? AND id=?`,
		t.Unix(), userID, id,
	)
	return err
}

// TouchMemoryItems sets last_used_at=now for each given ID.
// IDs that don't exist or belong to other users are silently skipped.
func TouchMemoryItems(db *sql.DB, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	now := time.Now().Unix()
	for _, id := range ids {
		if _, err := db.Exec(
			`UPDATE memory_items SET last_used_at=? WHERE id=?`,
			now, id,
		); err != nil {
			return err
		}
	}
	return nil
}
```

- [ ] **Step 4: Run the tests — expect pass**

```bash
go test ./internal/store/... -run "TestUpdateMemoryItem|TestSetMemoryPin|TestSetMemoryExpiry|TestTouchMemoryItems|TestListMemoryItemsPinnedFirst" -v
```

Expected: all 5 tests PASS.

- [ ] **Step 5: Build everything to catch compile errors**

```bash
go build ./...
```

Expected: no output.

- [ ] **Step 6: Commit**

```bash
git add internal/store/memory.go internal/store/memory_test.go
git commit -m "feat: add UpdateMemoryItem, SetMemoryPin, SetMemoryExpiry, TouchMemoryItems"
```

---

## Task 3: Store — User Settings

**Files:**
- Create: `internal/store/settings.go`
- Create: `internal/store/settings_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/store/settings_test.go`:

```go
package store_test

import (
	"testing"

	"tether/internal/store"
)

func TestGetSetUserSetting(t *testing.T) {
	d := openTestDB(t) // defined in memory_test.go (same package)
	val, ok, err := store.GetUserSetting(d, 1, "confirm_strictness")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("expected no value before set")
	}
	_ = val

	if err := store.SetUserSetting(d, 1, "confirm_strictness", "always"); err != nil {
		t.Fatal(err)
	}
	val, ok, err = store.GetUserSetting(d, 1, "confirm_strictness")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || val != "always" {
		t.Fatalf("expected 'always', got %q ok=%v", val, ok)
	}
}

func TestGetAllUserSettings(t *testing.T) {
	d := openTestDB(t)
	_ = store.SetUserSetting(d, 1, "k1", "v1")
	_ = store.SetUserSetting(d, 1, "k2", "v2")
	m, err := store.GetAllUserSettings(d, 1)
	if err != nil {
		t.Fatal(err)
	}
	if m["k1"] != "v1" || m["k2"] != "v2" {
		t.Fatalf("unexpected settings: %v", m)
	}
}
```

- [ ] **Step 2: Run tests — expect failure**

```bash
go test ./internal/store/... -run "TestGetSetUserSetting|TestGetAllUserSettings" -v 2>&1 | head -20
```

Expected: compile error — `store.GetUserSetting undefined`.

- [ ] **Step 3: Create `internal/store/settings.go`**

```go
package store

import (
	"database/sql"
	"errors"
)

func GetUserSetting(db *sql.DB, userID int64, key string) (string, bool, error) {
	var val string
	err := db.QueryRow(
		`SELECT value FROM user_settings WHERE user_id=? AND key=?`,
		userID, key,
	).Scan(&val)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return val, true, nil
}

func SetUserSetting(db *sql.DB, userID int64, key, value string) error {
	_, err := db.Exec(
		`INSERT OR REPLACE INTO user_settings(user_id, key, value) VALUES (?,?,?)`,
		userID, key, value,
	)
	return err
}

func GetAllUserSettings(db *sql.DB, userID int64) (map[string]string, error) {
	rows, err := db.Query(
		`SELECT key, value FROM user_settings WHERE user_id=?`,
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		m[k] = v
	}
	return m, rows.Err()
}
```

- [ ] **Step 4: Run the tests — expect pass**

```bash
go test ./internal/store/... -run "TestGetSetUserSetting|TestGetAllUserSettings" -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/store/settings.go internal/store/settings_test.go
git commit -m "feat: add user_settings store (GetUserSetting, SetUserSetting, GetAllUserSettings)"
```

---

## Task 4: Admin TUI

**Files:**
- Create: `internal/tui/admin.go`

- [ ] **Step 1: Create `internal/tui/admin.go`**

```go
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
	ctx *SessionContext
	tab adminTab
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
			req, err := http.NewRequest(http.MethodGet, base+"/api/v1/check", nil)
			if err == nil {
				resp, err2 := http.DefaultClient.Do(req)
				if err2 != nil {
					checkLine = styleError.Render("check failed: " + err2.Error())
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
			m.getTabViewport(msg.tab).GotoTop()
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
	vp := m.getTabViewport(m.tab)
	*vp, cmd = vp.Update(msg)
	return m, cmd
}

// switchTab changes the active tab and triggers a load.
func (m adminModel) switchTab(tab adminTab) (adminModel, tea.Cmd) {
	m.tab = tab
	return m, m.loadTabCmd(tab)
}

// getTabViewport returns a pointer to the viewport for the given tab.
func (m *adminModel) getTabViewport(tab adminTab) *viewport.Model {
	switch tab {
	case adminTabAudit:
		return &m.audit
	case adminTabUsers:
		return &m.users
	case adminTabJobs:
		return &m.jobs
	default:
		return &m.signal
	}
}

func (m *adminModel) setTabContent(tab adminTab, content string) {
	vp := m.getTabViewport(tab)
	vp.SetContent(content)
	vp.GotoTop()
}

func (m *adminModel) rebuildUsersViewport() {
	var b strings.Builder
	b.WriteString(styleTitle.Render("users") + "\n\n")
	for i, u := range m.userList {
		roleTag := styleDim.Render("[" + u.Role + "]")
		line := u.Username + "  " + roleTag
		if i == m.userSel {
			line = styleTabActive.Render(" "+u.Username+" ") + "  " + roleTag
		} else {
			line = "  " + line
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
```

- [ ] **Step 2: Build to verify it compiles**

```bash
go build ./internal/tui/...
```

Expected: no output.

- [ ] **Step 3: Commit**

```bash
git add internal/tui/admin.go
git commit -m "feat: add adminModel with audit/users/jobs/signal sub-tabs"
```

---

## Task 5: Wire Admin into app.go

**Files:**
- Modify: `internal/tui/app.go`

- [ ] **Step 1: Add `viewAdmin` constant and `admin adminModel` field**

In `app.go`, find the `viewMode` const block:

```go
const (
	viewLogin viewMode = iota
	viewSignup
	viewChat
	viewMemory
	viewSettings
)
```

Replace with:

```go
const (
	viewLogin viewMode = iota
	viewSignup
	viewChat
	viewMemory
	viewSettings
	viewAdmin
)
```

Find the `appModel` struct and add the `admin` field after `memory`:

```go
	auth   authModel
	chat   chatModel
	memory memoryModel
	admin  adminModel
```

In `NewAppModel`, after `m.memory = newMemoryModel()` add:

```go
	m.admin = newAdminModel(ctx)
```

- [ ] **Step 2: Update WindowSizeMsg handler**

Find the `case tea.WindowSizeMsg:` block and add:

```go
		m.admin = m.admin.withSize(m.w, m.h-1)
```

after the existing `m.auth = m.auth.withSize(...)` line.

- [ ] **Step 3: Update headerButtons to show admin tab for admin users**

Find `func (m appModel) headerButtons() []headerButton` and replace the logged-in return:

```go
	return []headerButton{
		{ID: "chat", Label: "Chat"},
		{ID: "memory", Label: "Memory"},
		{ID: "settings", Label: "Settings"},
	}
```

with:

```go
	btns := []headerButton{
		{ID: "chat", Label: "Chat"},
		{ID: "memory", Label: "Memory"},
		{ID: "settings", Label: "Settings"},
	}
	if m.user != nil && m.user.Role == "admin" {
		btns = append(btns, headerButton{ID: "admin", Label: "Admin"})
	}
	return btns
```

- [ ] **Step 4: Update renderHeader to handle admin active state**

In `renderHeader`, find the active-state switch and add:

```go
		case "admin":
			active = m.view == viewAdmin
```

- [ ] **Step 5: Update mouse click handler for admin tab**

In the `case tea.MouseClickMsg:` block, inside the header-button switch, add after the "settings" case:

```go
				case "admin":
					m.view = viewAdmin
					m.admin = m.admin.withSize(m.w, m.h-1)
					return m, m.admin.loadCmd()
```

- [ ] **Step 6: Update Update() delegate switch**

Find the `switch m.view {` block that delegates to sub-models and add:

```go
	case viewAdmin:
		m.admin, cmd = m.admin.Update(msg)
```

- [ ] **Step 7: Update View() to render admin**

In `View()`, find the body switch and add:

```go
	case viewAdmin:
		body = m.admin.View()
```

Also update the Settings case from the stub to keep it (it will be replaced in Task 7, leave for now):

```go
	case viewSettings:
		body = tea.NewView(styleTitle.Render("Settings") + "\n" + styleDim.Render("(not implemented yet)"))
```

- [ ] **Step 8: Build and verify**

```bash
go build ./...
```

Expected: no output.

- [ ] **Step 9: Commit**

```bash
git add internal/tui/app.go
git commit -m "feat: wire admin view into app — tab appears for admin users"
```

---

## Task 6: Memory TUI — Cursor-Driven CRUD

**Files:**
- Modify: `internal/tui/memory.go`

- [ ] **Step 1: Replace `internal/tui/memory.go` entirely**

```go
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

	// edit mode
	editing   bool
	editInput textinput.Model

	// expiry input mode
	expiryMode  bool
	expiryInput textinput.Model

	status    string
	statusErr bool

	w, h int
	err  error
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
	m.editInput.SetWidth(w - 4)
	m.expiryInput.SetWidth(w - 4)
	m.rebuild()
	return m
}

func (m memoryModel) loadCmd() tea.Cmd {
	db := m.db
	userID := m.userID
	return func() tea.Msg {
		if db == nil || userID == 0 {
			return memoryLoadedMsg{Items: nil}
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
			m.err = msg.Err
			m.status = "error: " + msg.Err.Error()
			m.statusErr = true
		} else {
			m.err = nil
			m.items = msg.Items
			if m.cursor >= len(m.items) {
				m.cursor = max(0, len(m.items)-1)
			}
			m.status = ""
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
					} else {
						m.status = "expiry set"
						m.statusErr = false
					}
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
					} else {
						m.status = "saved"
						m.statusErr = false
					}
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

		// Normal mode
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
				} else {
					m.status = ""
				}
				m.rebuild()
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
				} else {
					m.items = append(m.items[:m.cursor], m.items[m.cursor+1:]...)
					if m.cursor >= len(m.items) {
						m.cursor = max(0, len(m.items)-1)
					}
					m.status = ""
				}
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

	// Group items by kind for display
	kinds := []string{"task", "fact", "pref"}
	kindLabels := map[string]string{"task": "tasks", "fact": "facts", "pref": "preferences"}

	// Build a flat index mapping cursor positions to item indices
	// (so we know which line corresponds to cursor)
	lineToItem := map[int]int{} // line number -> items index
	lineCount := 0

	for _, kind := range kinds {
		var kindItems []int
		for i, it := range m.items {
			if it.Kind == kind {
				kindItems = append(kindItems, i)
			}
		}
		if len(kindItems) == 0 {
			continue
		}

		// Section header
		header := styleAccent.Render(kindLabels[kind]) + "  " + styleDim.Render("─────────────────────")
		b.WriteString(header + "\n")
		lineCount++

		for _, idx := range kindItems {
			it := m.items[idx]
			lineToItem[lineCount] = idx

			// Build prefix indicators
			prefix := "  "
			if it.Pinned {
				prefix = styleSenderBot.Render("●") + " "
			} else if it.LastUsedAt != nil && time.Since(*it.LastUsedAt) < time.Hour {
				prefix = styleAccent.Render("→") + " "
			}

			// Build suffix
			suffix := ""
			if it.ExpiresAt != nil {
				days := int(time.Until(*it.ExpiresAt).Hours() / 24)
				suffix = "  " + styleDim.Render(fmt.Sprintf("(exp %dd)", days))
			}

			content := it.Content
			// Edit mode replaces content with input
			if m.editing && idx == m.cursor {
				content = m.editInput.View()
			}

			line := prefix + content + suffix
			if idx == m.cursor && !m.editing {
				// Highlight selected line
				available := m.viewport.Width() - lipgloss.Width(prefix) - lipgloss.Width(suffix)
				if available < 0 {
					available = 0
				}
				highlighted := styleTitle.Width(available).Render(content)
				line = prefix + highlighted + suffix
			}

			b.WriteString(line + "\n")
			lineCount++
		}
		b.WriteString("\n")
		lineCount++
	}

	if m.expiryMode {
		b.WriteString("\n" + m.expiryInput.View() + "\n")
	}

	// Status line
	hint := styleDim.Render("↑↓·move  e·edit  p·pin  x·expire  D·delete  r·refresh")
	if m.status != "" {
		if m.statusErr {
			hint = styleError.Render(m.status)
		} else {
			hint = styleInfo.Render(m.status) + "  " + hint
		}
	}

	content := strings.TrimRight(b.String(), "\n") + "\n\n" + hint
	m.viewport.SetContent(content)

	// Scroll to keep cursor visible — find the cursor's line
	for line, idx := range lineToItem {
		if idx == m.cursor {
			// Try to center the cursor line in the viewport
			targetOffset := line - m.viewport.Height()/2
			if targetOffset < 0 {
				targetOffset = 0
			}
			m.viewport.SetYOffset(targetOffset)
			break
		}
	}
}

func (m memoryModel) View() tea.View {
	return tea.NewView(m.viewport.View())
}
```

- [ ] **Step 2: Build to verify**

```bash
go build ./internal/tui/...
```

Expected: no output.

- [ ] **Step 3: Build everything**

```bash
go build ./...
```

Expected: no output.

- [ ] **Step 4: Commit**

```bash
git add internal/tui/memory.go
git commit -m "feat: replace read-only memory view with cursor-driven CRUD (edit, pin, expiry, delete)"
```

---

## Task 7: Settings TUI

**Files:**
- Create: `internal/tui/settings.go`
- Modify: `internal/tui/app.go`

- [ ] **Step 1: Create `internal/tui/settings.go`**

```go
package tui

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"gopkg.in/yaml.v3"

	"tether/internal/proactive"
	"tether/internal/store"
)

// settingLine is one row in the settings view (either a section header or an editable field).
type settingLine struct {
	isHeader bool
	label    string // display label
	value    string // current rendered value
	kind     string // "toggle", "enum", "text", "action", "display"
	key      string // identifies which field this is (see applyEdit)
	options  []string
}

type settingsModel struct {
	db     *sql.DB
	userID int64
	ctx    *SessionContext

	w, h int

	rules    proactive.Rules
	settings map[string]string // user_settings key-value cache

	signalLinked bool
	signalNumber string

	lines  []settingLine
	cursor int // index into editable lines only

	editing   bool
	editInput textinput.Model

	status    string
	statusErr bool
}

type settingsLoadedMsg struct {
	rules    proactive.Rules
	settings map[string]string
	sigNum   string
	sigOK    bool
	err      error
}

type settingsSavedMsg struct {
	err error
}

func newSettingsModel(ctx *SessionContext) settingsModel {
	ei := textinput.New()
	ei.Prompt = "> "
	ei.CharLimit = 100

	m := settingsModel{
		ctx:       ctx,
		editInput: ei,
		settings:  map[string]string{},
	}
	return m
}

func (m settingsModel) withDB(db *sql.DB, userID int64) settingsModel {
	m.db = db
	m.userID = userID
	return m
}

func (m settingsModel) withSize(w, h int) settingsModel {
	m.w, m.h = w, h
	m.editInput.SetWidth(max(20, w-20))
	return m
}

func (m settingsModel) loadCmd() tea.Cmd {
	db := m.db
	userID := m.userID
	ctx := m.ctx
	return func() tea.Msg {
		rules := proactive.DefaultRules()
		if y, _, ok, err := store.GetProactiveRulesYAML(db, userID); err == nil && ok {
			_ = yaml.Unmarshal([]byte(y), &rules)
		}
		settings, err := store.GetAllUserSettings(db, userID)
		if err != nil {
			return settingsLoadedMsg{err: err}
		}
		sigNum, sigOK, _ := store.GetSignalNumber(db, userID)
		_ = ctx
		return settingsLoadedMsg{rules: rules, settings: settings, sigNum: sigNum, sigOK: sigOK}
	}
}

func (m settingsModel) Update(msg tea.Msg) (settingsModel, tea.Cmd) {
	switch msg := msg.(type) {
	case settingsLoadedMsg:
		if msg.err != nil {
			m.status = "load error: " + msg.err.Error()
			m.statusErr = true
			return m, nil
		}
		m.rules = msg.rules
		m.settings = msg.settings
		m.signalLinked = msg.sigOK
		m.signalNumber = msg.sigNum
		m.buildLines()
		m.status = ""
		return m, nil

	case settingsSavedMsg:
		if msg.err != nil {
			m.status = "save error: " + msg.err.Error()
			m.statusErr = true
		} else {
			m.status = "saved"
			m.statusErr = false
		}
		return m, nil

	case tea.KeyPressMsg:
		if m.editing {
			switch msg.String() {
			case "enter":
				m.editing = false
				m.editInput.Blur()
				if err := m.applyTextEdit(); err != nil {
					m.status = "invalid: " + err.Error()
					m.statusErr = true
				} else {
					m.status = ""
					m.buildLines()
					return m, m.saveCmd()
				}
				m.buildLines()
				return m, nil
			case "esc":
				m.editing = false
				m.editInput.Blur()
				m.buildLines()
				return m, nil
			}
			var cmd tea.Cmd
			m.editInput, cmd = m.editInput.Update(msg)
			return m, cmd
		}

		// Normal navigation
		editables := m.editableIndices()
		switch msg.String() {
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(editables)-1 {
				m.cursor++
			}
		case "e":
			if m.cursor < len(editables) {
				line := m.lines[editables[m.cursor]]
				if line.kind == "text" {
					m.editing = true
					m.editInput.SetValue(line.value)
					m.editInput.Focus()
					m.editInput.CursorEnd()
				}
			}
		case " ", "left", "right":
			if m.cursor < len(editables) {
				line := m.lines[editables[m.cursor]]
				switch line.kind {
				case "toggle":
					m.applyToggle(line.key)
					m.buildLines()
					return m, m.saveCmd()
				case "enum":
					m.cycleEnum(line.key, line.options, msg.String() == "left")
					m.buildLines()
					return m, m.saveCmd()
				}
			}
		case "L":
			// Signal link
			if !m.signalLinked {
				return m, func() tea.Msg {
					code, err := store.CreateSignalLinkCode(m.db, m.userID, 10*60*1e9) // 10 min
					if err != nil {
						return settingsSavedMsg{err: err}
					}
					acct := strings.TrimSpace(m.ctx.Config.Signal.AccountNumber)
					if acct == "" {
						acct = "<signal account not configured>"
					}
					_ = code
					_ = acct
					// Surface link code in status
					return settingsSignalLinkMsg{code: code, acct: acct}
				}
			}
		case "U":
			// Signal unlink
			if m.signalLinked {
				return m, func() tea.Msg {
					err := store.UnlinkSignalNumber(m.db, m.userID)
					return settingsSavedMsg{err: err}
				}
			}
		case "r":
			return m, m.loadCmd()
		}
	}
	return m, nil
}

type settingsSignalLinkMsg struct{ code, acct string }

func (m *settingsModel) applyTextEdit() error {
	editables := m.editableIndices()
	if m.cursor >= len(editables) {
		return nil
	}
	line := m.lines[editables[m.cursor]]
	val := strings.TrimSpace(m.editInput.Value())
	switch line.key {
	case "daily_brief_time", "open_loops_time":
		if len(val) != 5 || val[2] != ':' {
			return fmt.Errorf("expected HH:MM")
		}
		if line.key == "daily_brief_time" {
			m.rules.DailyBrief.Time = val
		} else {
			m.rules.OpenLoops.Time = val
		}
	case "inactivity_minutes":
		n, err := strconv.Atoi(val)
		if err != nil || n < 0 {
			return fmt.Errorf("expected positive integer")
		}
		m.rules.Inactivity.Minutes = n
	case "retention_days":
		n, err := strconv.Atoi(val)
		if err != nil || n < 1 {
			return fmt.Errorf("expected positive integer")
		}
		m.settings["message_retention_days"] = val
	}
	return nil
}

func (m *settingsModel) applyToggle(key string) {
	switch key {
	case "daily_brief_enabled":
		m.rules.DailyBrief.Enabled = !m.rules.DailyBrief.Enabled
	case "open_loops_enabled":
		m.rules.OpenLoops.Enabled = !m.rules.OpenLoops.Enabled
	case "inactivity_enabled":
		m.rules.Inactivity.Enabled = !m.rules.Inactivity.Enabled
	}
}

func (m *settingsModel) cycleEnum(key string, options []string, reverse bool) {
	cur := m.settings[key]
	idx := 0
	for i, o := range options {
		if o == cur {
			idx = i
			break
		}
	}
	if reverse {
		idx = (idx - 1 + len(options)) % len(options)
	} else {
		idx = (idx + 1) % len(options)
	}
	m.settings[key] = options[idx]
}

func (m settingsModel) saveCmd() tea.Cmd {
	rules := m.rules
	settings := m.settings
	db := m.db
	userID := m.userID
	return func() tea.Msg {
		b, err := yaml.Marshal(rules)
		if err != nil {
			return settingsSavedMsg{err: err}
		}
		if err := store.SetProactiveRulesYAML(db, userID, string(b)); err != nil {
			return settingsSavedMsg{err: err}
		}
		for k, v := range settings {
			if err := store.SetUserSetting(db, userID, k, v); err != nil {
				return settingsSavedMsg{err: err}
			}
		}
		return settingsSavedMsg{}
	}
}

// buildLines constructs the flat list of displayable/editable lines.
func (m *settingsModel) buildLines() {
	enabled := func(b bool) string {
		if b {
			return styleInfo.Render("on")
		}
		return styleDim.Render("off")
	}
	enumVal := func(key, def string) string {
		if v, ok := m.settings[key]; ok {
			return v
		}
		return def
	}

	m.lines = []settingLine{
		{isHeader: true, label: "proactive rules"},
		{label: "daily brief", kind: "toggle", key: "daily_brief_enabled", value: enabled(m.rules.DailyBrief.Enabled)},
		{label: "daily brief time", kind: "text", key: "daily_brief_time", value: m.rules.DailyBrief.Time},
		{label: "open loops", kind: "toggle", key: "open_loops_enabled", value: enabled(m.rules.OpenLoops.Enabled)},
		{label: "open loops time", kind: "text", key: "open_loops_time", value: m.rules.OpenLoops.Time},
		{label: "inactivity nudge", kind: "toggle", key: "inactivity_enabled", value: enabled(m.rules.Inactivity.Enabled)},
		{label: "inactivity after (min)", kind: "text", key: "inactivity_minutes", value: strconv.Itoa(m.rules.Inactivity.Minutes)},

		{isHeader: true, label: "confirmation"},
		{label: "strictness", kind: "enum", key: "confirm_strictness",
			value:   enumVal("confirm_strictness", "destructive-only"),
			options: []string{"always", "destructive-only", "never"}},

		{isHeader: true, label: "signal"},
		m.signalLine(),

		{isHeader: true, label: "retention"},
		{label: "message history (days)", kind: "text", key: "retention_days",
			value: enumVal("message_retention_days", "90")},
	}
}

func (m *settingsModel) signalLine() settingLine {
	if m.signalLinked {
		return settingLine{
			label: "linked number",
			kind:  "display",
			value: m.signalNumber + "  " + styleDim.Render("U·unlink"),
		}
	}
	return settingLine{
		label: "status",
		kind:  "display",
		value: styleDim.Render("not linked") + "  " + styleDim.Render("L·link"),
	}
}

// editableIndices returns indices into m.lines that are interactive (not headers or display).
func (m *settingsModel) editableIndices() []int {
	var out []int
	for i, l := range m.lines {
		if !l.isHeader && l.kind != "display" {
			out = append(out, i)
		}
	}
	return out
}

func (m settingsModel) View() tea.View {
	if len(m.lines) == 0 {
		return tea.NewView(styleDim.Render("loading…"))
	}

	editables := m.editableIndices()
	activeLine := -1
	if m.cursor < len(editables) {
		activeLine = editables[m.cursor]
	}

	labelW := 26
	var b strings.Builder

	for i, line := range m.lines {
		if line.isHeader {
			b.WriteString("\n" + styleAccent.Render(line.label) + "  " + styleDim.Render("──────────────────") + "\n")
			continue
		}

		label := lipgloss.NewStyle().Width(labelW).Foreground(colorMuted).Render(line.label)
		val := line.value

		if i == activeLine && m.editing && line.kind == "text" {
			val = m.editInput.View()
		}

		row := label + "  " + val
		if i == activeLine && !m.editing {
			row = styleTabActive.Render(" "+line.label+" ") +
				strings.Repeat(" ", max(0, labelW-len(line.label)-2)) +
				"  " + val
		}
		b.WriteString(row + "\n")
	}

	// Status / hint
	hint := styleDim.Render("↑↓·move  space·toggle/cycle  e·edit  r·reload")
	if m.status != "" {
		if m.statusErr {
			hint = styleError.Render(m.status)
		} else {
			hint = styleInfo.Render(m.status) + "  " + hint
		}
	}

	content := b.String() + "\n" + hint
	if m.w > 0 {
		return tea.NewView(lipgloss.NewStyle().Width(m.w).Render(content))
	}
	return tea.NewView(content)
}
```

- [ ] **Step 2: Build to verify**

```bash
go build ./internal/tui/...
```

Expected: no output.

- [ ] **Step 3: Wire settings into app.go — add field and init**

In `appModel` struct, add after `admin adminModel`:

```go
	settings settingsModel
```

In `NewAppModel`, after `m.admin = newAdminModel(ctx)`:

```go
	m.settings = newSettingsModel(ctx)
```

- [ ] **Step 4: Wire settings into WindowSizeMsg**

In the `case tea.WindowSizeMsg:` block, add:

```go
		m.settings = m.settings.withSize(m.w, m.h-1)
```

- [ ] **Step 5: Wire settings into loginSuccessMsg and authSubmitMsg**

In both `loginSuccessMsg` and `authSubmitMsg` handlers (where `m.user` and `m.conv` are set), add:

```go
		m.settings = m.settings.withDB(m.ctx.DB, m.user.ID).withSize(m.w, m.h-1)
```

- [ ] **Step 6: Wire settings into mouse click for settings tab**

In the header mouse-click switch, find `case "settings":` and replace:

```go
			case "settings":
				m.view = viewSettings
```

with:

```go
			case "settings":
				m.view = viewSettings
				m.settings = m.settings.withSize(m.w, m.h-1)
				return m, m.settings.loadCmd()
```

- [ ] **Step 7: Wire settings Update and View**

In the view-delegate switch in `Update`, replace or add:

```go
	case viewSettings:
		m.settings, cmd = m.settings.Update(msg)
```

In `View()`, replace the settings stub:

```go
	case viewSettings:
		body = m.settings.View()
```

- [ ] **Step 8: Handle settingsSignalLinkMsg in app.go Update**

In the top-level `Update` switch, add a case to surface the link code in chat:

```go
	case settingsSignalLinkMsg:
		if m.conv != nil {
			resp := "Signal link code: " + msg.code + "\nSend from your phone to: " + msg.acct + "\n(Expires in ~10 minutes.)"
			_ = store.AddMessage(m.ctx.DB, m.conv.ID, "assistant", resp)
			m.chat = m.chat.appendLocal("System", resp)
		}
		return m, nil
```

- [ ] **Step 9: Build everything**

```bash
go build ./...
```

Expected: no output.

- [ ] **Step 10: Commit**

```bash
git add internal/tui/settings.go internal/tui/app.go
git commit -m "feat: add settings view (proactive rules, confirmation, signal, retention)"
```

---

## Task 8: Agent — TouchMemoryItems

**Files:**
- Modify: `internal/agent/context.go`

- [ ] **Step 1: Update `buildContextMessages` to touch used items**

Replace the memory-injection block in `internal/agent/context.go` (lines 22–53) with:

```go
	// Long-term memory (facts/prefs/tasks)
	mem, err := store.ListMemoryItems(a.db, userID, "", 200)
	if err == nil {
		facts := make([]string, 0, 20)
		prefs := make([]string, 0, 20)
		tasks := make([]string, 0, 20)
		var usedIDs []int64
		for _, it := range mem {
			switch it.Kind {
			case "fact":
				if len(facts) < 15 {
					facts = append(facts, it.Content)
					usedIDs = append(usedIDs, it.ID)
				}
			case "pref":
				if len(prefs) < 15 {
					prefs = append(prefs, it.Content)
					usedIDs = append(usedIDs, it.ID)
				}
			case "task":
				if len(tasks) < 10 {
					tasks = append(tasks, it.Content)
					usedIDs = append(usedIDs, it.ID)
				}
			}
		}
		// Mark items as used so the memory UI can show "in prompt" indicator.
		_ = store.TouchMemoryItems(a.db, usedIDs)
		if len(facts) > 0 {
			msgs = append(msgs, openrouter.Message{Role: "system", Content: openrouter.Text("User facts (top):\n- " + strings.Join(facts, "\n- "))})
		}
		if len(prefs) > 0 {
			msgs = append(msgs, openrouter.Message{Role: "system", Content: openrouter.Text("User preferences (top):\n- " + strings.Join(prefs, "\n- "))})
		}
		if len(tasks) > 0 {
			msgs = append(msgs, openrouter.Message{Role: "system", Content: openrouter.Text("Open tasks:\n- " + strings.Join(tasks, "\n- "))})
		}
	}
```

- [ ] **Step 2: Build to verify**

```bash
go build ./...
```

Expected: no output.

- [ ] **Step 3: Commit**

```bash
git add internal/agent/context.go
git commit -m "feat: touch memory items after injecting into prompt (last_used_at explainability)"
```

---

## Self-Review

**Spec coverage check:**

| Spec requirement | Implemented in |
|-----------------|---------------|
| Admin: audit log viewer | Task 4 (adminTabAudit) |
| Admin: user list + promote/demote | Task 4 (adminTabUsers) |
| Admin: jobs status | Task 4 (adminTabJobs) |
| Admin: Signal status | Task 4 (adminTabSignal) |
| Admin tab admin-only | Task 5 (headerButtons gated on role) |
| Memory: pinned column | Task 1 (migration), Task 2 (store) |
| Memory: expires_at column | Task 1, Task 2 |
| Memory: last_used_at column | Task 1, Task 2 |
| Memory: edit inline | Task 6 (`e` key) |
| Memory: pin/unpin | Task 6 (`p` key) |
| Memory: set expiry | Task 6 (`x` key) |
| Memory: delete | Task 6 (`D` key) |
| Memory: visual indicators | Task 6 (●, →, exp Xd) |
| Memory explainability (last_used_at) | Task 8 (TouchMemoryItems in context.go) |
| Settings: proactive rules editor | Task 7 |
| Settings: confirmation strictness | Task 7 |
| Settings: Signal link/unlink | Task 7 (L/U keys) |
| Settings: retention days | Task 7 |
| Settings wired into app | Task 7 steps 3–8 |
| user_settings migration | Task 1 |
| user_settings store | Task 3 |

**No placeholders found.** All steps contain actual code.

**Type consistency:** `store.MemoryItem` fields (`Pinned bool`, `ExpiresAt *time.Time`, `LastUsedAt *time.Time`) defined in Task 2 and used in Task 6. `store.GetProactiveRulesYAML` / `store.SetProactiveRulesYAML` used in Task 7 — both already exist in `internal/store/proactive_rules.go`. `store.GetSignalNumber` / `store.UnlinkSignalNumber` / `store.CreateSignalLinkCode` used in Task 7 — all exist in `internal/store/signal.go`. `store.ListUsers` / `store.SetUserRole` used in Task 4 — exist in `internal/store/admin.go`.
