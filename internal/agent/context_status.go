package agent

import (
	"context"
	"strings"

	"tether/internal/personality"
	"tether/internal/skills"
	"tether/internal/store"
)

type AttachedContextStatus struct {
	Available             bool
	PersonalityAttached   bool
	SummaryAttached       bool
	SummaryThroughID      int64
	HistoryMessages       int
	HistoryUserMessages   int
	HistoryAssistMessages int
	MemoryFacts           int
	MemoryPrefs           int
	MemoryTasks           int
	SkillsIndexAttached   bool
	InvokedSkills         int
	EstimatedTokens       int
	ContextLimit          int
	ContextPct            float64
}

func (a *Agent) attachedContextStatus(userID, convID int64) AttachedContextStatus {
	if a == nil || a.db == nil || userID == 0 {
		return AttachedContextStatus{}
	}

	sess := a.sessionFor(userID, convID)
	st := AttachedContextStatus{Available: true}
	tokenEstimate := 0

	if sess != nil {
		if p := loadPersonalityText(sess.Dirs, personality.AgentChat); strings.TrimSpace(p) != "" {
			st.PersonalityAttached = true
			tokenEstimate += estimateTextTokens(p)
		}
	}

	if convID != 0 {
		if sum, ok, err := store.GetConversationSummaryState(a.db, convID); err == nil && ok {
			if summaryRef := formatConversationSummaryReference(sum.Summary); strings.TrimSpace(summaryRef) != "" {
				st.SummaryAttached = true
				st.SummaryThroughID = sum.SummarizedThroughMessageID
				tokenEstimate += estimateTextTokens(summaryRef)
			}
		}
		afterID := st.SummaryThroughID
		if history, err := store.ListMessagesAfterID(a.db, convID, afterID); err == nil {
			for _, m := range history {
				switch m.Role {
				case "tool_call", "assistant_reasoning":
					continue
				case "user":
					st.HistoryUserMessages++
				case "assistant":
					st.HistoryAssistMessages++
				}
				st.HistoryMessages++
			}
			tokenEstimate += estimateMessagesTokens(history)
		}
	}

	if mem, err := store.ListMemoryItems(a.db, userID, "", 200); err == nil {
		facts := 0
		prefs := 0
		tasks := 0
		for _, it := range mem {
			switch it.Kind {
			case "fact":
				if facts < 15 {
					facts++
					tokenEstimate += estimateTextTokens(it.Content)
				}
			case "pref":
				if prefs < 15 {
					prefs++
					tokenEstimate += estimateTextTokens(it.Content)
				}
			case "task":
				if tasks < 10 {
					tasks++
					tokenEstimate += estimateTextTokens(it.Content)
				}
			}
		}
		st.MemoryFacts = facts
		st.MemoryPrefs = prefs
		st.MemoryTasks = tasks
	}

	if sess != nil && !sess.IsSubagent {
		mgr := skills.NewManager()
		if list, err := mgr.List(sess.Dirs); err == nil {
			idx := strings.TrimSpace(mgr.BuildIndexMessage(list))
			if idx != "" {
				st.SkillsIndexAttached = true
				tokenEstimate += estimateTextTokens(idx)
			}
		}
		const totalBudget = 25_000
		total := 0
		for i := len(sess.InvokedSkills) - 1; i >= 0; i-- {
			content := strings.TrimSpace(sess.InvokedSkills[i].Content)
			if content == "" {
				continue
			}
			msg := "Skill /" + sess.InvokedSkills[i].Name + " (invoked):\n" + content
			if total+len(msg) > totalBudget {
				break
			}
			total += len(msg)
			st.InvokedSkills++
			tokenEstimate += estimateTextTokens(msg)
		}
	}

	tokenEstimate += estimateTextTokens("(tether metadata; ignore)")
	st.EstimatedTokens = tokenEstimate
	if info := a.modelInfo(a.cfg.LLMModel()); info.ContextLength > 0 {
		st.ContextLimit = info.ContextLength
		st.ContextPct = contextPercent(tokenEstimate, info.ContextLength)
	}
	return st
}

func (a *Agent) AttachedContextStatus(ctx context.Context, userID, convID int64) AttachedContextStatus {
	_ = ctx
	return a.attachedContextStatus(userID, convID)
}
