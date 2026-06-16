package proactive

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"charm.land/log/v2"
	"gopkg.in/yaml.v3"

	"tether/internal/personality"
	"tether/internal/store"
	"tether/internal/subagents"
	"tether/internal/userspace"
)

const (
	maxNotificationsPerUserPerDay = 6
	defaultAgentTimeoutSeconds    = 120
)

// Built-in event names for AgentRule.Events.
const (
	EventLogin       = "login"
	EventUserMessage = "user_message"
	EventTaskChanged = "task_changed"
)

type Engine struct {
	db      *sql.DB
	llm     LLM
	subs    *subagents.Manager
	dataDir string

	selfRunner SelfScheduleRunner

	mu      sync.Mutex
	pending map[string]bool // key=userID:agentID:trigger:day
}

func NewEngine(db *sql.DB, llm LLM, selfRunner SelfScheduleRunner, subs *subagents.Manager, dataDir string) *Engine {
	return &Engine{db: db, llm: llm, subs: subs, dataDir: dataDir, selfRunner: selfRunner, pending: map[string]bool{}}
}

func (e *Engine) Tick(ctx context.Context, now time.Time) error {
	if e.db == nil {
		return nil
	}
	now = now.UTC()
	_ = store.AddAuditEvent(e.db, nil, "proactive_tick", fmt.Sprintf(`{"ts":"%s"}`, now.Format(time.RFC3339)))

	userIDs, err := store.ListUserIDs(e.db)
	if err != nil {
		return err
	}

	startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	dayKey := startOfDay.Format("2006-01-02")

	for _, uid := range userIDs {
		rules, _ := e.loadRulesForUser(uid)

		// Built-in rules
		if rules.DailyBrief.Enabled && dueAt(now, rules.DailyBrief.Time) {
			has, err := store.HasNotificationSince(e.db, uid, "daily_brief", startOfDay)
			if err != nil {
				return err
			}
			if !has {
				prompt := e.dailyBriefPrompt(uid)
				e.spawnIfNotPending(ctx, uid, "daily_brief", "time:"+rules.DailyBrief.Time, dayKey, "scheduled_due", prompt, 2*time.Minute, startOfDay)
			}
		}

		if rules.OpenLoops.Enabled && dueAt(now, rules.OpenLoops.Time) {
			has, err := store.HasNotificationSince(e.db, uid, "open_loops", startOfDay)
			if err != nil {
				return err
			}
			if !has {
				prompt := e.openLoopsPrompt(uid)
				e.spawnIfNotPending(ctx, uid, "open_loops", "time:"+rules.OpenLoops.Time, dayKey, "scheduled_due", prompt, 2*time.Minute, startOfDay)
			}
		}

		if rules.Inactivity.Enabled && rules.Inactivity.Minutes > 0 {
			last, ok, err := store.LatestUserMessageTime(e.db, uid)
			if err != nil {
				return err
			}
			if ok && time.Since(last) > time.Duration(rules.Inactivity.Minutes)*time.Minute {
				since := now.Add(-6 * time.Hour)
				has, err := store.HasNotificationSince(e.db, uid, "inactivity_nudge", since)
				if err != nil {
					return err
				}
				if !has {
					if c, err := store.CountNotificationsSince(e.db, uid, startOfDay); err == nil && c >= maxNotificationsPerUserPerDay {
						payload, _ := json.Marshal(map[string]any{"kind": "inactivity_nudge", "day": dayKey, "count": c, "max": maxNotificationsPerUserPerDay})
						_ = store.AddAuditEvent(e.db, &uid, "proactive_skip_rate_limit", string(payload))
					} else {
						_ = store.AddNotification(e.db, uid, "inactivity_nudge", fmt.Sprintf("You have been inactive for ~%d minutes. Anything you want to plan or review?", rules.Inactivity.Minutes))
						payload, _ := json.Marshal(map[string]any{"kind": "inactivity_nudge", "day": dayKey, "reason": fmt.Sprintf("inactive_minutes>%d", rules.Inactivity.Minutes)})
						_ = store.AddAuditEvent(e.db, &uid, "proactive_trigger", string(payload))
					}
				}
			}
		}

		// Custom scheduled agents
		for _, ar := range rules.Agents {
			if !ar.Enabled {
				continue
			}
			agID, ok := normalizeAgentID(ar.ID)
			if !ok {
				continue
			}

			for _, hhmm := range ar.ScheduleTimes {
				hhmm = strings.TrimSpace(hhmm)
				if hhmm == "" {
					continue
				}
				if !dueAt(now, hhmm) {
					continue
				}
				// A condition, when present, gates the scheduled run.
				if !e.conditionMet(ctx, uid, ar) {
					continue
				}
				trigger := "time:" + hhmm
				prompt := e.customAgentPrompt(uid, ar, agID, "scheduled", hhmm, nil)
				e.runCustomAgent(ctx, uid, ar, agID, "agent/"+agID, trigger, dayKey, "scheduled_due", prompt, startOfDay)
			}

			// Condition-only agents (no schedule/events/actions): poll the
			// predicate and run when it passes.
			if ar.Condition != nil && len(ar.ScheduleTimes) == 0 && len(ar.Events) == 0 && len(ar.Actions) == 0 {
				poll := ar.Condition.PollMinutes
				if poll <= 0 {
					poll = 5
				}
				if (now.Unix()/60)%int64(poll) == 0 && e.conditionMet(ctx, uid, ar) {
					trigger := conditionTrigger(now, ar)
					prompt := e.customAgentPrompt(uid, ar, agID, "condition", "met", nil)
					e.runCustomAgent(ctx, uid, ar, agID, "agent/"+agID, trigger, dayKey, "condition_met", prompt, startOfDay)
				}
			}
		}

		// One-off self-scheduled runs (created via the self.schedule tool).
		e.tickSelfSchedules(ctx, uid, now)
	}

	return nil
}

// TriggerEvent runs any configured agents that listen to the given built-in event.
// Generation is async when sub-agents are available; results are stored as notifications.
func (e *Engine) TriggerEvent(ctx context.Context, userID int64, event string, meta map[string]string) {
	e.triggerForAgents(ctx, userID, "event", strings.TrimSpace(strings.ToLower(event)), meta)
}

// TriggerAction runs any configured agents that listen to the given custom action.
// Generation is async when sub-agents are available; results are stored as notifications.
func (e *Engine) TriggerAction(ctx context.Context, userID int64, action string, meta map[string]string) {
	e.triggerForAgents(ctx, userID, "action", strings.TrimSpace(strings.ToLower(action)), meta)
}

// TriggerAgent runs a single configured agent by id.
func (e *Engine) TriggerAgent(ctx context.Context, userID int64, agentID string, meta map[string]string) {
	agID, ok := normalizeAgentID(agentID)
	if !ok {
		return
	}
	e.triggerForAgents(ctx, userID, "agent", agID, meta)
}

type AgentRunResult struct {
	AgentID string `json:"agent_id"`
	Kind    string `json:"kind"`
	Output  string `json:"output"`
}

// RunActionNow runs matching agents synchronously via the main LLM and returns their outputs.
// It also records the output as an already-delivered notification (for auditing/history) to avoid
// re-injecting it into the chat later.
func (e *Engine) RunActionNow(ctx context.Context, userID int64, action string, meta map[string]string) ([]AgentRunResult, error) {
	action = strings.TrimSpace(strings.ToLower(action))
	if action == "" {
		return nil, errors.New("action required")
	}
	if e.db == nil {
		return nil, errors.New("db not available")
	}
	if e.llm == nil {
		return nil, errors.New("llm not available")
	}

	now := time.Now().UTC()
	startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	dayKey := startOfDay.Format("2006-01-02")

	rules, _ := e.loadRulesForUser(userID)
	out := []AgentRunResult{}
	for _, ar := range rules.Agents {
		if !ar.Enabled || !containsFold(ar.Actions, action) {
			continue
		}
		agID, ok := normalizeAgentID(ar.ID)
		if !ok {
			continue
		}
		kind := "agent/" + agID
		trigger := "action:" + action
		prompt := e.customAgentPrompt(userID, ar, agID, "action", action, meta)
		text, ok2 := e.runCustomAgentNow(ctx, userID, ar, agID, kind, trigger, dayKey, prompt, startOfDay)
		if ok2 {
			out = append(out, AgentRunResult{AgentID: agID, Kind: kind, Output: text})
		}
	}
	if len(out) == 0 {
		return nil, errors.New("no matching proactive agents for action")
	}
	return out, nil
}

// RunAgentNow runs one configured agent by id synchronously.
func (e *Engine) RunAgentNow(ctx context.Context, userID int64, agentID string, meta map[string]string) (AgentRunResult, error) {
	agID, ok := normalizeAgentID(agentID)
	if !ok {
		return AgentRunResult{}, errors.New("invalid agent_id")
	}
	if e.db == nil {
		return AgentRunResult{}, errors.New("db not available")
	}
	if e.llm == nil {
		return AgentRunResult{}, errors.New("llm not available")
	}

	now := time.Now().UTC()
	startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	dayKey := startOfDay.Format("2006-01-02")

	rules, _ := e.loadRulesForUser(userID)
	for _, ar := range rules.Agents {
		if !ar.Enabled {
			continue
		}
		arID, ok := normalizeAgentID(ar.ID)
		if !ok || arID != agID {
			continue
		}
		kind := "agent/" + agID
		trigger := "agent:" + agID
		prompt := e.customAgentPrompt(userID, ar, agID, "agent", agID, meta)
		text, ok2 := e.runCustomAgentNow(ctx, userID, ar, agID, kind, trigger, dayKey, prompt, startOfDay)
		if !ok2 {
			return AgentRunResult{}, errors.New("agent run skipped by limits (cooldown/max_per_day/dedup)")
		}
		return AgentRunResult{AgentID: agID, Kind: kind, Output: text}, nil
	}
	return AgentRunResult{}, errors.New("agent not found")
}

func (e *Engine) triggerForAgents(ctx context.Context, userID int64, triggerType string, triggerName string, meta map[string]string) {
	if e.db == nil || triggerName == "" {
		return
	}

	now := time.Now().UTC()
	startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	dayKey := startOfDay.Format("2006-01-02")

	rules, _ := e.loadRulesForUser(userID)
	for _, ar := range rules.Agents {
		if !ar.Enabled {
			continue
		}
		agID, ok := normalizeAgentID(ar.ID)
		if !ok {
			continue
		}

		match := false
		switch triggerType {
		case "event":
			match = containsFold(ar.Events, triggerName)
		case "action":
			match = containsFold(ar.Actions, triggerName)
		case "agent":
			match = strings.EqualFold(agID, triggerName)
		}
		if !match {
			continue
		}
		// A condition, when present, also gates event/action-triggered runs.
		if !e.conditionMet(ctx, userID, ar) {
			continue
		}

		trigger := triggerType + ":" + triggerName
		prompt := e.customAgentPrompt(userID, ar, agID, triggerType, triggerName, meta)
		e.runCustomAgent(ctx, userID, ar, agID, "agent/"+agID, trigger, dayKey, "triggered", prompt, startOfDay)
	}
}

func (e *Engine) runCustomAgent(ctx context.Context, userID int64, ar AgentRule, normalizedID string, kind string, trigger string, dayKey string, reason string, prompt string, startOfDay time.Time) {
	// Per-user notification rate limit (reuse existing rule).
	if c, err := store.CountNotificationsSince(e.db, userID, startOfDay); err == nil && c >= maxNotificationsPerUserPerDay {
		payload, _ := json.Marshal(map[string]any{"kind": kind, "agent_id": normalizedID, "trigger": trigger, "day": dayKey, "count": c, "max": maxNotificationsPerUserPerDay})
		_ = store.AddAuditEvent(e.db, &userID, "proactive_skip_rate_limit", string(payload))
		return
	}

	maxPerDay := ar.MaxPerDay
	if maxPerDay <= 0 {
		maxPerDay = 1
	}
	if maxPerDay > 0 {
		if c, err := store.CountProactiveRunsForDay(e.db, userID, normalizedID, dayKey); err == nil && c >= maxPerDay {
			payload, _ := json.Marshal(map[string]any{"kind": kind, "agent_id": normalizedID, "trigger": trigger, "day": dayKey, "count": c, "max": maxPerDay})
			_ = store.AddAuditEvent(e.db, &userID, "proactive_skip_agent_limit", string(payload))
			return
		}
	}

	if ar.CooldownMinutes > 0 {
		if t, ok, err := store.LatestProactiveRunTime(e.db, userID, normalizedID); err == nil && ok {
			if time.Since(t) < time.Duration(ar.CooldownMinutes)*time.Minute {
				payload, _ := json.Marshal(map[string]any{"kind": kind, "agent_id": normalizedID, "trigger": trigger, "cooldown_minutes": ar.CooldownMinutes})
				_ = store.AddAuditEvent(e.db, &userID, "proactive_skip_cooldown", string(payload))
				return
			}
		}
	}

	// Deduplicate: once per day per (agent, trigger).
	started, err := store.TryStartProactiveRun(e.db, userID, normalizedID, trigger, dayKey)
	if err != nil {
		log.Warn("proactive run start failed", "error", err)
		return
	}
	if !started {
		return
	}

	timeout := time.Duration(defaultAgentTimeoutSeconds) * time.Second
	if ar.TimeoutSeconds > 0 {
		timeout = time.Duration(ar.TimeoutSeconds) * time.Second
	}
	if timeout < 5*time.Second {
		timeout = 5 * time.Second
	}

	e.spawnIfNotPending(ctx, userID, kind, trigger, dayKey, reason, prompt, timeout, startOfDay)
}

// runCustomAgentNow is a synchronous variant used for manual triggers (e.g. proactive.run tool).
// It returns ran=false if the run is skipped by limits/dedup.
func (e *Engine) runCustomAgentNow(ctx context.Context, userID int64, ar AgentRule, normalizedID string, kind string, trigger string, dayKey string, prompt string, startOfDay time.Time) (out string, ran bool) {
	// Reuse the same limits as async runs.
	if c, err := store.CountNotificationsSince(e.db, userID, startOfDay); err == nil && c >= maxNotificationsPerUserPerDay {
		payload, _ := json.Marshal(map[string]any{"kind": kind, "agent_id": normalizedID, "trigger": trigger, "day": dayKey, "count": c, "max": maxNotificationsPerUserPerDay})
		_ = store.AddAuditEvent(e.db, &userID, "proactive_skip_rate_limit", string(payload))
		return "", false
	}

	maxPerDay := ar.MaxPerDay
	if maxPerDay <= 0 {
		maxPerDay = 1
	}
	if maxPerDay > 0 {
		if c, err := store.CountProactiveRunsForDay(e.db, userID, normalizedID, dayKey); err == nil && c >= maxPerDay {
			payload, _ := json.Marshal(map[string]any{"kind": kind, "agent_id": normalizedID, "trigger": trigger, "day": dayKey, "count": c, "max": maxPerDay})
			_ = store.AddAuditEvent(e.db, &userID, "proactive_skip_agent_limit", string(payload))
			return "", false
		}
	}

	if ar.CooldownMinutes > 0 {
		if t, ok, err := store.LatestProactiveRunTime(e.db, userID, normalizedID); err == nil && ok {
			if time.Since(t) < time.Duration(ar.CooldownMinutes)*time.Minute {
				payload, _ := json.Marshal(map[string]any{"kind": kind, "agent_id": normalizedID, "trigger": trigger, "cooldown_minutes": ar.CooldownMinutes})
				_ = store.AddAuditEvent(e.db, &userID, "proactive_skip_cooldown", string(payload))
				return "", false
			}
		}
	}

	started, err := store.TryStartProactiveRun(e.db, userID, normalizedID, trigger, dayKey)
	if err != nil {
		return "", false
	}
	if !started {
		return "", false
	}

	timeout := time.Duration(defaultAgentTimeoutSeconds) * time.Second
	if ar.TimeoutSeconds > 0 {
		timeout = time.Duration(ar.TimeoutSeconds) * time.Second
	}
	if timeout < 5*time.Second {
		timeout = 5 * time.Second
	}

	payload, _ := json.Marshal(map[string]any{"kind": kind, "day": dayKey, "reason": "manual_now", "trigger": trigger})
	_ = store.AddAuditEvent(e.db, &userID, "proactive_trigger", string(payload))

	ctx2, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	text, err := e.llm.RunProactivePromptForUser(ctx2, userID, prompt)
	if err != nil {
		out = "(proactive error: " + err.Error() + ")"
		_ = store.AddNotificationDelivered(e.db, userID, kind, out)
		return out, true
	}
	out = strings.TrimSpace(text)
	if out == "" {
		out = "(empty)"
	}
	_ = store.AddNotificationDelivered(e.db, userID, kind, out)
	return out, true
}

func (e *Engine) pendingKey(userID int64, kind string, trigger string, dayKey string) string {
	return fmt.Sprintf("%d:%s:%s:%s", userID, kind, trigger, dayKey)
}

func (e *Engine) spawnIfNotPending(ctx context.Context, userID int64, kind string, trigger string, dayKey string, reason string, prompt string, timeout time.Duration, startOfDay time.Time) {
	key := e.pendingKey(userID, kind, trigger, dayKey)
	e.mu.Lock()
	if e.pending[key] {
		e.mu.Unlock()
		return
	}
	e.pending[key] = true
	e.mu.Unlock()

	payload, _ := json.Marshal(map[string]any{"kind": kind, "day": dayKey, "reason": reason, "trigger": trigger})
	_ = store.AddAuditEvent(e.db, &userID, "proactive_trigger", string(payload))

	if e.subs != nil {
		run := e.subs.Spawn(userID, subagents.RunRequest{Prompt: prompt})
		go func() {
			defer func() {
				e.mu.Lock()
				delete(e.pending, key)
				e.mu.Unlock()
			}()

			ctx2, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			r, ok := e.subs.Wait(ctx2, run.ID)
			if !ok || r == nil {
				return
			}
			if r.Status == subagents.StatusDone {
				_ = store.AddNotification(e.db, userID, kind, strings.TrimSpace(r.Result))
			} else if r.Status == subagents.StatusError {
				_ = store.AddNotification(e.db, userID, kind, "(proactive error: "+r.Err+")")
			}
		}()
		return
	}

	go func() {
		defer func() {
			e.mu.Lock()
			delete(e.pending, key)
			e.mu.Unlock()
		}()
		if e.llm == nil {
			return
		}
		ctx2, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		text, err := e.llm.RunProactivePromptForUser(ctx2, userID, prompt)
		if err != nil {
			_ = store.AddNotification(e.db, userID, kind, "(proactive error)")
			return
		}
		_ = store.AddNotification(e.db, userID, kind, strings.TrimSpace(text))
	}()
}

func (e *Engine) dailyBriefPrompt(userID int64) string {
	convID, ok, _ := store.FirstConversationID(e.db, userID)
	sum := ""
	if ok {
		if s2, _, ok2, _ := store.GetConversationSummary(e.db, convID); ok2 {
			sum = strings.TrimSpace(s2)
		}
	}

	tasks, _ := store.ListMemoryItems(e.db, userID, "task", 50)
	var tb strings.Builder
	for _, t := range tasks {
		tb.WriteString("- ")
		tb.WriteString(t.Content)
		tb.WriteString("\n")
	}

	prompt := "Write a short daily brief. Include:\n- top priorities\n- open tasks\n- suggested next actions\nKeep it under 120 words.\n\n"
	if sum != "" {
		prompt += formatSummaryForPrompt(sum)
	}
	if tb.Len() > 0 {
		prompt += "Tasks:\n" + tb.String() + "\n"
	}
	return e.prependPersonality(userID, personality.AgentProactiveDailyBrief, prompt)
}

func (e *Engine) openLoopsPrompt(userID int64) string {
	convID, ok, _ := store.FirstConversationID(e.db, userID)
	sum := ""
	if ok {
		if s2, _, ok2, _ := store.GetConversationSummary(e.db, convID); ok2 {
			sum = strings.TrimSpace(s2)
		}
	}

	tasks, _ := store.ListMemoryItems(e.db, userID, "task", 50)
	var tb strings.Builder
	for _, t := range tasks {
		tb.WriteString("- ")
		tb.WriteString(t.Content)
		tb.WriteString("\n")
	}

	prompt := "You are checking for open loops and follow-ups.\n" +
		"Write a short message (<= 90 words) that helps the user close open loops.\n" +
		"Ask at most 2 clarifying questions and suggest at most 3 next actions.\n\n"
	if sum != "" {
		prompt += formatSummaryForPrompt(sum)
	}
	if tb.Len() > 0 {
		prompt += "Open tasks:\n" + tb.String() + "\n"
	}
	return e.prependPersonality(userID, personality.AgentProactiveOpenLoops, prompt)
}

func (e *Engine) customAgentPrompt(userID int64, ar AgentRule, normalizedID string, triggerType string, triggerName string, meta map[string]string) string {
	prompt := BuildCustomAgentPrompt(e.db, userID, ar, triggerType, triggerName, meta)
	if strings.TrimSpace(normalizedID) == "" {
		return prompt
	}
	return e.prependPersonality(userID, personality.ProactiveAgentKey(normalizedID), prompt)
}

func (e *Engine) loadRulesForUser(userID int64) (Rules, error) {
	rules := DefaultRules()
	loaded := false
	loadedFromDB := false
	var dbUpdatedAt time.Time

	// Prefer SQLite-stored rules (spec requirement).
	if y, upd, ok, err := store.GetProactiveRulesYAML(e.db, userID); err == nil && ok {
		if err := yaml.Unmarshal([]byte(y), &rules); err == nil {
			loaded = true
			loadedFromDB = true
			dbUpdatedAt = upd
		}
	}

	// Allow legacy per-user config file to override if it is newer than the DB row.
	if strings.TrimSpace(e.dataDir) != "" {
		cfgDir := userspace.ForUser(e.dataDir, userID).Config
		path := DefaultRulesPath(cfgDir)
		if fi, err := os.Stat(path); err == nil {
			useFile := !loaded || (loadedFromDB && !dbUpdatedAt.IsZero() && fi.ModTime().After(dbUpdatedAt.Add(2*time.Second)))
			if useFile {
				if b, err := os.ReadFile(path); err == nil {
					if err := yaml.Unmarshal(b, &rules); err == nil {
						loaded = true
						_ = store.SetProactiveRulesYAML(e.db, userID, string(b))
					}
				}
			}
		}
	}

	if !loaded {
		b, _ := yaml.Marshal(DefaultRules())
		_ = store.EnsureDefaultProactiveRulesYAML(e.db, userID, string(b))
	}

	if strings.TrimSpace(rules.DailyBrief.Time) == "" {
		rules.DailyBrief.Time = "08:00"
	}
	if strings.TrimSpace(rules.OpenLoops.Time) == "" {
		rules.OpenLoops.Time = "20:00"
	}

	// Custom proactive agents are folder-based only; legacy YAML `agents:` is not
	// supported. The agent list comes entirely from the user's agents/ directory.
	rules.Agents = nil
	if strings.TrimSpace(e.dataDir) != "" {
		agentsDir := userspace.ForUser(e.dataDir, userID).Agents
		rules.Agents = loadFolderAgents(agentsDir)
	}

	e.ensurePersonalities(userID, rules)
	return rules, nil
}

func containsFold(list []string, s string) bool {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return false
	}
	for _, it := range list {
		if strings.TrimSpace(strings.ToLower(it)) == s {
			return true
		}
	}
	return false
}

func normalizeAgentID(id string) (string, bool) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", false
	}
	id = strings.ToLower(id)
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			continue
		}
		return "", false
	}
	return id, true
}

func dueAt(now time.Time, hhmm string) bool {
	hhmm = strings.TrimSpace(hhmm)
	parts := strings.Split(hhmm, ":")
	if len(parts) != 2 {
		return false
	}
	hh := atoi(parts[0])
	mm := atoi(parts[1])
	return now.Hour() == hh && now.Minute() == mm
}

func atoi(s string) int {
	v := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0
		}
		v = v*10 + int(r-'0')
	}
	return v
}
