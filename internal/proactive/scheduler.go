package proactive

import (
	"context"
	"database/sql"
	"time"

	"charm.land/log/v2"

	"tether/internal/subagents"
)

// Scheduler runs periodic proactive checks.
//
// It is a thin wrapper around Engine that ticks on a fixed interval.
type Scheduler struct {
	eng      *Engine
	interval time.Duration
}

func NewScheduler(db *sql.DB, llm LLM, selfRunner SelfScheduleRunner, subs *subagents.Manager, dataDir string, interval time.Duration) *Scheduler {
	if interval <= 0 {
		interval = 1 * time.Minute
	}
	return &Scheduler{eng: NewEngine(db, llm, selfRunner, subs, dataDir), interval: interval}
}

// RegisterNotifier registers external-channel deliverers (e.g. the Discord/Signal
// gateways) used to push proactive/self-scheduled messages to the user.
func (s *Scheduler) RegisterNotifier(ns ...Notifier) {
	s.eng.RegisterNotifier(ns...)
}

func (s *Scheduler) Start(ctx context.Context) {
	t := time.NewTicker(s.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := s.eng.Tick(ctx, time.Now().UTC()); err != nil {
				log.Warn("proactive tick error", "error", err)
			}
		}
	}
}
