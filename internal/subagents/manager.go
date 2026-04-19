package subagents

import (
	"context"
	"strings"
	"sync"
	"time"
)

type Status string

const (
	StatusQueued   Status = "queued"
	StatusRunning  Status = "running"
	StatusDone     Status = "done"
	StatusError    Status = "error"
	StatusCanceled Status = "canceled"
)

type Run struct {
	ID        string
	UserID    int64
	CreatedAt time.Time
	StartedAt time.Time
	EndedAt   time.Time

	Prompt string
	Config RunRequest

	Status Status
	Result string
	Err    string

	CurrentText string
	UpdatedAt   time.Time
	History     []HistoryEntry

	done chan struct{}
}

type Runner interface {
	Run(ctx context.Context, userID int64, req RunRequest, emit func(ProgressEvent)) (string, error)
}

type RunRequest struct {
	Prompt       string
	AllowedTools []string
	SkillName    string
	SkillArgs    string
}

type HistoryEntry struct {
	At   time.Time `json:"at"`
	Type string    `json:"type"`
	Text string    `json:"text,omitempty"`
}

type ProgressEvent struct {
	Type string
	Text string
}

const maxHistoryEntries = 24

// Manager provides async sub-agent runs.
// Initial implementation is in-memory; later we can persist in SQLite.
type Manager struct {
	runner Runner

	mu   sync.Mutex
	runs map[string]*Run
}

func NewManager(r Runner) *Manager {
	return &Manager{runner: r, runs: map[string]*Run{}}
}

func (m *Manager) Spawn(userID int64, req RunRequest) *Run {
	r := &Run{
		ID:        newID(),
		UserID:    userID,
		CreatedAt: time.Now(),
		Prompt:    req.Prompt,
		Config:    req,
		Status:    StatusQueued,
		done:      make(chan struct{}),
	}
	m.mu.Lock()
	m.runs[r.ID] = r
	m.mu.Unlock()

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()

		m.mu.Lock()
		r.Status = StatusRunning
		r.StartedAt = time.Now()
		r.UpdatedAt = r.StartedAt
		r.appendHistoryLocked(HistoryEntry{At: r.StartedAt, Type: "status", Text: "subagent started"})
		m.mu.Unlock()

		res, err := m.runner.Run(ctx, userID, req, func(ev ProgressEvent) {
			m.mu.Lock()
			defer m.mu.Unlock()
			now := time.Now()
			r.UpdatedAt = now
			switch strings.TrimSpace(ev.Type) {
			case "assistant":
				r.CurrentText = truncateProgressText(ev.Text, 1200)
			case "tool_call", "tool_result", "status", "done", "error":
				r.appendHistoryLocked(HistoryEntry{At: now, Type: ev.Type, Text: truncateProgressText(ev.Text, 300)})
			}
		})
		m.mu.Lock()
		r.EndedAt = time.Now()
		r.UpdatedAt = r.EndedAt
		if err != nil {
			r.Status = StatusError
			r.Err = err.Error()
			r.appendHistoryLocked(HistoryEntry{At: r.EndedAt, Type: "error", Text: truncateProgressText(err.Error(), 300)})
			close(r.done)
			m.mu.Unlock()
			return
		}
		r.Status = StatusDone
		r.Result = res
		r.CurrentText = truncateProgressText(res, 1200)
		r.appendHistoryLocked(HistoryEntry{At: r.EndedAt, Type: "done", Text: truncateProgressText(res, 300)})
		close(r.done)
		m.mu.Unlock()
	}()

	return r
}

func (m *Manager) Get(id string) (*Run, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.runs[id]
	if !ok {
		return nil, false
	}
	copy := *r
	if r.History != nil {
		copy.History = append([]HistoryEntry(nil), r.History...)
	}
	return &copy, true
}

func (m *Manager) GetForUser(userID int64, id string) (*Run, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.runs[id]
	if !ok || r == nil || r.UserID != userID {
		return nil, false
	}
	copy := *r
	if r.History != nil {
		copy.History = append([]HistoryEntry(nil), r.History...)
	}
	return &copy, true
}

// Wait blocks until a run is finished (done/error/canceled) or ctx is canceled.
func (m *Manager) Wait(ctx context.Context, id string) (*Run, bool) {
	m.mu.Lock()
	r := m.runs[id]
	if r == nil {
		m.mu.Unlock()
		return nil, false
	}
	done := r.done
	m.mu.Unlock()

	select {
	case <-ctx.Done():
		return nil, true
	case <-done:
		res, _ := m.Get(id)
		return res, true
	}
}

func newID() string {
	// small helper without extra deps
	return time.Now().UTC().Format("20060102T150405.000000000Z")
}

func (r *Run) appendHistoryLocked(entry HistoryEntry) {
	entry.Text = strings.TrimSpace(entry.Text)
	if entry.At.IsZero() {
		entry.At = time.Now()
	}
	if entry.Type == "" {
		entry.Type = "status"
	}
	if entry.Type == "assistant" {
		return
	}
	r.History = append(r.History, entry)
	if len(r.History) > maxHistoryEntries {
		r.History = append([]HistoryEntry(nil), r.History[len(r.History)-maxHistoryEntries:]...)
	}
}

func truncateProgressText(s string, max int) string {
	s = strings.TrimSpace(s)
	if max <= 0 || len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
