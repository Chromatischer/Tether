package agent

import (
	"crypto/rand"
	"encoding/base32"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"tether/internal/store"
)

type confirmEntry struct {
	UserID int64
	Scope  string
	Reason string

	CreatedAt time.Time
	Confirmed bool
	Used      bool
	Rejected  bool
}

type confirmManager struct {
	mu    sync.Mutex
	items map[string]*confirmEntry
	ttl   time.Duration
}

func newConfirmManager() *confirmManager {
	return &confirmManager{items: map[string]*confirmEntry{}, ttl: 10 * time.Minute}
}

func (m *confirmManager) Request(userID int64, scope string, reason string) string {
	now := time.Now()
	tok := randomToken(8)
	m.mu.Lock()
	m.pruneLocked(now)
	m.items[tok] = &confirmEntry{UserID: userID, Scope: scope, Reason: reason, CreatedAt: now}
	m.mu.Unlock()
	return tok
}

func (m *confirmManager) Confirm(userID int64, token string) bool {
	now := time.Now()
	m.mu.Lock()
	m.pruneLocked(now)
	defer m.mu.Unlock()
	it := m.items[token]
	if it == nil {
		return false
	}
	if it.UserID != userID {
		return false
	}
	if time.Since(it.CreatedAt) > m.ttl {
		delete(m.items, token)
		return false
	}
	if it.Used || it.Rejected {
		return false
	}
	it.Confirmed = true
	return true
}

func (m *confirmManager) Reject(userID int64, token string) bool {
	now := time.Now()
	m.mu.Lock()
	m.pruneLocked(now)
	defer m.mu.Unlock()
	it := m.items[token]
	if it == nil {
		return false
	}
	if it.UserID != userID {
		return false
	}
	if time.Since(it.CreatedAt) > m.ttl {
		delete(m.items, token)
		return false
	}
	if it.Used {
		return false
	}
	it.Rejected = true
	it.Used = true
	return true
}

func (m *confirmManager) Consume(userID int64, token string, scope string) bool {
	now := time.Now()
	m.mu.Lock()
	m.pruneLocked(now)
	defer m.mu.Unlock()
	it := m.items[token]
	if it == nil {
		return false
	}
	if it.UserID != userID {
		return false
	}
	if time.Since(it.CreatedAt) > m.ttl {
		delete(m.items, token)
		return false
	}
	if it.Scope != scope {
		return false
	}
	if !it.Confirmed || it.Used || it.Rejected {
		return false
	}
	it.Used = true
	return true
}

func (m *confirmManager) Peek(token string) (confirmEntry, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	it := m.items[token]
	if it == nil {
		return confirmEntry{}, false
	}
	copy := *it
	return copy, true
}

func (m *confirmManager) pruneLocked(now time.Time) {
	for tok, it := range m.items {
		if it == nil {
			delete(m.items, tok)
			continue
		}
		if now.Sub(it.CreatedAt) > m.ttl {
			delete(m.items, tok)
		}
	}
}

func randomToken(nbytes int) string {
	b := make([]byte, nbytes)
	_, _ = rand.Read(b)
	return strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b))
}

// ConfirmToken confirms a pending destructive-action token.
func (a *Agent) ConfirmToken(userID int64, token string) bool {
	if a.confirm == nil {
		return false
	}
	ok := a.confirm.Confirm(userID, token)

	// Best-effort audit.
	if a.db != nil {
		payload := map[string]any{"token": strings.TrimSpace(token), "ok": ok}
		if it, ok2 := a.confirm.Peek(strings.TrimSpace(token)); ok2 {
			payload["scope"] = it.Scope
			payload["reason"] = truncateString(it.Reason, 300)
			payload["confirmed"] = it.Confirmed
			payload["used"] = it.Used
			payload["rejected"] = it.Rejected
			payload["age_seconds"] = int(time.Since(it.CreatedAt).Seconds())
		}
		b, _ := json.Marshal(payload)
		_ = store.AddAuditEvent(a.db, &userID, "confirm_confirm", string(b))
	}

	return ok
}

func (a *Agent) rejectConfirmToken(userID int64, token string) bool {
	if a.confirm == nil {
		return false
	}
	ok := a.confirm.Reject(userID, token)
	if a.db != nil {
		payload := map[string]any{"token": strings.TrimSpace(token), "ok": ok}
		if it, ok2 := a.confirm.Peek(strings.TrimSpace(token)); ok2 {
			payload["scope"] = it.Scope
			payload["reason"] = truncateString(it.Reason, 300)
			payload["confirmed"] = it.Confirmed
			payload["used"] = it.Used
			payload["rejected"] = it.Rejected
			payload["age_seconds"] = int(time.Since(it.CreatedAt).Seconds())
		}
		b, _ := json.Marshal(payload)
		_ = store.AddAuditEvent(a.db, &userID, "confirm_reject", string(b))
	}
	return ok
}

func truncateString(s string, max int) string {
	s = strings.TrimSpace(s)
	if max <= 0 {
		max = 200
	}
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
