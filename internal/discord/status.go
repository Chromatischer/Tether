package discord

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"tether/internal/agent"
)

func renderSessionStatusDiscord(st agent.SessionStatus) string {
	var b strings.Builder
	b.WriteString("Session status\n")
	b.WriteString("- session_id: ")
	b.WriteString(emptyDash(st.SessionID))
	b.WriteString("\n- conversation_id: ")
	b.WriteString(strconv.FormatInt(st.ConversationID, 10))
	b.WriteString("\n- user_id: ")
	b.WriteString(strconv.FormatInt(st.UserID, 10))
	b.WriteString("\n- started: ")
	b.WriteString(formatStatusTimeUTC(st.StartedAt))
	b.WriteString("\n- last_activity: ")
	b.WriteString(formatStatusTimeUTC(st.LastActivityAt))
	b.WriteString("\n- age: ")
	b.WriteString(formatStatusDurationDiscord(st.Age))
	b.WriteString("\n- idle: ")
	b.WriteString(formatStatusDurationDiscord(st.Idle))
	b.WriteString("\n- runtime_session: ")
	if st.HasRuntimeSession {
		b.WriteString("active")
	} else {
		b.WriteString("none")
	}
	b.WriteString("\n- tool_calls: ")
	if st.HasRuntimeSession {
		b.WriteString(strconv.Itoa(st.TotalToolCalls))
	} else {
		b.WriteString("unknown")
	}
	b.WriteString("\n- usage_source: ")
	b.WriteString(st.UsageSource)
	if !st.LastUsageAt.IsZero() {
		b.WriteString("\n- last_usage_at: ")
		b.WriteString(formatStatusTimeUTC(st.LastUsageAt))
	}
	b.WriteString("\n- cost_usd: ")
	if st.UsageSource == "none" {
		b.WriteString("unknown")
	} else {
		b.WriteString(fmt.Sprintf("%.6f", st.TotalCost))
	}
	b.WriteString("\n- model: ")
	if st.UsageSource == "none" {
		b.WriteString("unknown")
	} else {
		b.WriteString(emptyDash(st.LastModel))
	}
	b.WriteString("\n- last_request_context: ")
	if st.LastContextLimit > 0 {
		b.WriteString(fmt.Sprintf("%d / %d (%.1f%%)", st.LastInputTokens, st.LastContextLimit, st.LastContextPct))
	} else {
		if st.UsageSource == "none" {
			b.WriteString("unknown")
		} else {
			b.WriteString("context limit unknown")
		}
	}
	b.WriteString("\n- attached_context:")
	if st.AttachedContext.Available {
		b.WriteString("\n  personality: ")
		b.WriteString(boolWord(st.AttachedContext.PersonalityAttached))
		b.WriteString("\n  summary: ")
		b.WriteString(boolWord(st.AttachedContext.SummaryAttached))
		b.WriteString("\n  history_messages: ")
		b.WriteString(strconv.Itoa(st.AttachedContext.HistoryMessages))
		b.WriteString("\n  memory_facts: ")
		b.WriteString(strconv.Itoa(st.AttachedContext.MemoryFacts))
		b.WriteString("\n  memory_prefs: ")
		b.WriteString(strconv.Itoa(st.AttachedContext.MemoryPrefs))
		b.WriteString("\n  memory_tasks: ")
		b.WriteString(strconv.Itoa(st.AttachedContext.MemoryTasks))
		b.WriteString("\n  skills_index: ")
		b.WriteString(boolWord(st.AttachedContext.SkillsIndexAttached))
		b.WriteString("\n  invoked_skills: ")
		b.WriteString(strconv.Itoa(st.AttachedContext.InvokedSkills))
		b.WriteString("\n  estimated_attached_tokens: ")
		b.WriteString(strconv.Itoa(st.AttachedContext.EstimatedTokens))
		if st.AttachedContext.ContextLimit > 0 {
			b.WriteString(fmt.Sprintf(" / %d (%.1f%%)", st.AttachedContext.ContextLimit, st.AttachedContext.ContextPct))
		}
	} else {
		b.WriteString(" unavailable")
	}
	b.WriteString("\n- input_tokens: ")
	if st.UsageSource == "none" {
		b.WriteString("unknown")
	} else {
		b.WriteString(strconv.Itoa(st.TotalInputTokens))
	}
	b.WriteString("\n- output_tokens: ")
	if st.UsageSource == "none" {
		b.WriteString("unknown")
	} else {
		b.WriteString(strconv.Itoa(st.TotalOutputTokens))
	}
	b.WriteString("\n- total_tokens: ")
	if st.UsageSource == "none" {
		b.WriteString("unknown")
	} else {
		b.WriteString(strconv.Itoa(st.TotalTokens))
	}
	return b.String()
}

func formatStatusTimeUTC(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.UTC().Format(time.RFC3339)
}

func formatStatusDurationDiscord(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	return d.Round(time.Second).String()
}

func emptyDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}

func boolWord(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}
