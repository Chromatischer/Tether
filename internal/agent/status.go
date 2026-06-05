package agent

import (
	"time"

	"tether/internal/store"
)

type SessionStatus struct {
	SessionID         string
	UserID            int64
	ConversationID    int64
	StartedAt         time.Time
	LastActivityAt    time.Time
	Age               time.Duration
	Idle              time.Duration
	TotalToolCalls    int
	TotalInputTokens  int
	TotalOutputTokens int
	TotalTokens       int
	TotalCost         float64
	LastModel         string
	LastInputTokens   int
	LastContextLimit  int
	LastContextPct    float64
	CompactThreshold  int
	UsageSource       string
	LastUsageAt       time.Time
	HasRuntimeSession bool
	AttachedContext   AttachedContextStatus
}

func (a *Agent) SessionStatus(userID, convID int64) SessionStatus {
	now := time.Now().UTC()

	st := SessionStatus{
		UserID:         userID,
		ConversationID: convID,
		UsageSource:    "none",
	}

	a.mu.Lock()
	s := a.sessions[convID]
	if s != nil {
		st.HasRuntimeSession = true
		startedAt := s.StartedAt
		if startedAt.IsZero() {
			startedAt = now
		}
		lastActivityAt := s.LastActivityAt
		if lastActivityAt.IsZero() {
			lastActivityAt = startedAt
		}
		st.SessionID = s.SessionID
		st.StartedAt = startedAt
		st.LastActivityAt = lastActivityAt
		st.Age = now.Sub(startedAt)
		st.Idle = now.Sub(lastActivityAt)
		st.TotalToolCalls = s.TotalToolCalls
		st.TotalInputTokens = s.TotalInputTokens
		st.TotalOutputTokens = s.TotalOutputTokens
		st.TotalTokens = s.TotalTokens
		st.TotalCost = s.TotalCost
		st.LastModel = s.LastModel
		st.LastInputTokens = s.LastInputTokens
		st.LastContextLimit = s.LastContextLimit
		st.LastContextPct = contextPercent(s.LastInputTokens, s.LastContextLimit)
		if s.TotalTokens > 0 || s.LastInputTokens > 0 || s.TotalCost > 0 {
			st.UsageSource = "runtime"
		}
	}
	a.mu.Unlock()

	modelLimit := a.modelInfo(a.cfg.LLMModel()).ContextLength
	if modelLimit <= 0 {
		modelLimit = 128000
	}
	st.CompactThreshold = min(int(float64(modelLimit)*0.80), 200_000)

	if st.UsageSource == "runtime" || a == nil || a.db == nil {
		st.AttachedContext = a.attachedContextStatus(userID, convID)
		return st
	}
	if usage, ok, err := store.LatestConversationLLMUsage(a.db, userID, convID); err == nil && ok {
		st.TotalInputTokens = usage.SumInputTokens
		st.TotalOutputTokens = usage.SumOutputTokens
		st.TotalTokens = usage.SumTotalTokens
		st.TotalCost = usage.SumCost
		st.LastModel = usage.Model
		st.LastInputTokens = usage.InputTokens
		st.LastUsageAt = usage.LastSeenAt
		if info := a.modelInfo(usage.Model); info.ContextLength > 0 {
			st.LastContextLimit = info.ContextLength
		}
		st.LastContextPct = contextPercent(st.LastInputTokens, st.LastContextLimit)
		st.UsageSource = "audit"
	}
	st.AttachedContext = a.attachedContextStatus(userID, convID)
	return st
}

func contextPercent(inputTokens, contextLimit int) float64 {
	if inputTokens <= 0 || contextLimit <= 0 {
		return 0
	}
	return float64(inputTokens) / float64(contextLimit) * 100
}
