package subagents

import (
	"context"
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

	Status Status
	Result string
	Err    string

	done chan struct{}
}

type Runner interface {
	Run(ctx context.Context, userID int64, prompt string) (string, error)
}

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

func (m *Manager) Spawn(userID int64, prompt string) *Run {
	r := &Run{ID: newID(), UserID: userID, CreatedAt: time.Now(), Prompt: prompt, Status: StatusQueued, done: make(chan struct{})}
	m.mu.Lock()
	m.runs[r.ID] = r
	m.mu.Unlock()

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()

		m.mu.Lock()
		r.Status = StatusRunning
		r.StartedAt = time.Now()
		m.mu.Unlock()

		res, err := m.runner.Run(ctx, userID, prompt)
		m.mu.Lock()
		r.EndedAt = time.Now()
		if err != nil {
			r.Status = StatusError
			r.Err = err.Error()
			close(r.done)
			m.mu.Unlock()
			return
		}
		r.Status = StatusDone
		r.Result = res
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
