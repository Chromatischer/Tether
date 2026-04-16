package personality

import (
	"strings"
)

// Agent keys map to per-user personality files under:
//
//	config/agents/<agent_key>/PERSONALITY.md
//
// Agent keys are slash-separated segments, each segment matching: [a-z0-9_-]+
const (
	AgentChat                = "chat"
	AgentProactiveDailyBrief = "proactive/daily_brief"
	AgentProactiveOpenLoops  = "proactive/open_loops"
)

// IsValidAgentKey returns true if key is a safe slash-separated agent key.
func IsValidAgentKey(key string) bool {
	key = strings.TrimSpace(key)
	if key == "" {
		return false
	}
	for _, seg := range strings.Split(key, "/") {
		if seg == "" {
			return false
		}
		for _, r := range seg {
			if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
				continue
			}
			return false
		}
	}
	return true
}

// NormalizeID normalizes a single-segment agent id (used for custom proactive agents).
// It matches proactive.normalizeAgentID behavior.
func NormalizeID(id string) (string, bool) {
	id = strings.TrimSpace(strings.ToLower(id))
	if id == "" {
		return "", false
	}
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			continue
		}
		return "", false
	}
	return id, true
}

func ProactiveAgentKey(id string) string {
	id, _ = NormalizeID(id)
	return "proactive/" + id
}

// DefaultMarkdown returns a minimal starter personality document for an agent.
// The model is expected to iterate on this over time.
func DefaultMarkdown(agentKey string) string {
	agentKey = strings.TrimSpace(agentKey)
	switch agentKey {
	case AgentChat:
		return strings.TrimSpace(chatDefault)
	case AgentProactiveDailyBrief:
		return strings.TrimSpace(dailyBriefDefault)
	case AgentProactiveOpenLoops:
		return strings.TrimSpace(openLoopsDefault)
	default:
		if strings.HasPrefix(agentKey, "proactive/") {
			return strings.TrimSpace(customProactiveDefault)
		}
		return strings.TrimSpace(genericDefault)
	}
}

const chatDefault = `# Personality

## Role
You are Tether: a calm, highly practical personal assistant.

## Tone
- Direct, no preamble.
- Concise by default; expand only when useful.
- Friendly but not chatty.

## How you work
- Use tools first; don’t ask for info you can look up.
- Prefer concrete next actions over long explanations.
- When risk is high or intent is ambiguous: stop and confirm.

## Self-edit policy
You may update this file to reflect stable user preferences (tone, structure, defaults). Keep it short. Do not add secrets.`

const dailyBriefDefault = `# Personality

## Goal
Deliver a compact daily brief that is worth the interruption.

## Style
- Bullet-heavy, action-oriented.
- Highlight the single most important next action.
- Avoid motivational fluff.

## Constraints
- Keep to the requested word limit.
- No speculative claims without evidence.`

const openLoopsDefault = `# Personality

## Goal
Help the user close open loops with minimal back-and-forth.

## Style
- Identify 1–3 concrete follow-ups.
- Ask at most 2 clarifying questions.
- Assume the user is busy; make it skimmable.`

const customProactiveDefault = `# Personality

## Goal
Be useful in proactive mode: observe, summarize, and suggest next actions.

## Style
- Compact.
- Actionable.
- Conservative when uncertain.

## Self-edit policy
You may refine this personality over time to better match what the user finds helpful.`

const genericDefault = `# Personality

Be direct, helpful, and safe. Prefer actionable output over long explanations.
`
