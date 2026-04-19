package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"tether/internal/llm/openrouter"
)

func (a *Agent) storePendingConfirmation(p *pendingConfirmation) {
	if p == nil || strings.TrimSpace(p.Token) == "" {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.prunePendingLocked(time.Now())
	for tok, existing := range a.pendingRuns {
		if existing == nil {
			delete(a.pendingRuns, tok)
			continue
		}
		if existing.UserID == p.UserID && existing.ConversationID == p.ConversationID {
			delete(a.pendingRuns, tok)
			_ = a.rejectConfirmToken(p.UserID, tok)
		}
	}
	a.pendingRuns[p.Token] = p
}

func (a *Agent) HasPendingConfirmation(userID, convID int64) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.prunePendingLocked(time.Now())
	for _, p := range a.pendingRuns {
		if p != nil && p.UserID == userID && p.ConversationID == convID {
			return true
		}
	}
	return false
}

func (a *Agent) PendingConfirmationToken(userID, convID int64) (string, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.prunePendingLocked(time.Now())
	for tok, p := range a.pendingRuns {
		if p != nil && p.UserID == userID && p.ConversationID == convID {
			return tok, true
		}
	}
	return "", false
}

// HasPendingConfirmationToken reports whether the given confirmation token is currently
// associated with a suspended tool execution (i.e. a /confirm should RESUME work).
//
// If this returns false, the token may still be a valid standalone token created via
// confirm.request (if used outside a paused run), or any other mechanism that creates standalone tokens.
func (a *Agent) HasPendingConfirmationToken(userID int64, token string) bool {
	token = strings.TrimSpace(token)
	if token == "" {
		return false
	}
	if a == nil {
		return false
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	a.prunePendingLocked(time.Now())
	p := a.pendingRuns[token]
	return p != nil && p.UserID == userID
}

func (a *Agent) RejectPendingConfirmation(userID, convID int64) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.prunePendingLocked(time.Now())
	for tok, p := range a.pendingRuns {
		if p == nil {
			delete(a.pendingRuns, tok)
			continue
		}
		if p.UserID != userID || p.ConversationID != convID {
			continue
		}
		delete(a.pendingRuns, tok)
		return a.rejectConfirmToken(userID, tok)
	}
	return false
}

func (a *Agent) ResumeConfirmedStream(ctx context.Context, userID int64, token string, emit func(StreamEvent)) (Reply, int64, bool, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return Reply{}, 0, false, fmt.Errorf("confirmation token required")
	}
	if !a.ConfirmToken(userID, token) {
		return Reply{}, 0, false, nil
	}

	a.mu.Lock()
	a.prunePendingLocked(time.Now())
	p := a.pendingRuns[token]
	if p != nil {
		delete(a.pendingRuns, token)
	}
	a.mu.Unlock()
	if p == nil {
		return Reply{}, 0, false, nil
	}

	sess := cloneSession(p.Session)
	items := append([]openrouter.ResponseItem{}, p.Items...)
	call := p.Call
	call.Arguments = injectConfirmToken(call.Arguments, token)

	nm := newToolNameMap(nil)
	if sess != nil {
		nm = newToolNameMap(sess.Registry)
	}
	toolOutputs, infos, pause := a.executeFunctionCalls(ctx, sess, []openrouter.ResponseItem{call}, nm)
	if pause != nil {
		a.storePendingConfirmation(&pendingConfirmation{
			UserID:         p.UserID,
			ConversationID: p.ConversationID,
			Token:          pause.Token,
			Scope:          pause.Scope,
			Session:        cloneSession(sess),
			Items:          append([]openrouter.ResponseItem{}, items...),
			Call:           pause.Call,
			ToolCalls:      append([]ToolCallInfo{}, p.ToolCalls...),
			CreatedAt:      time.Now(),
		})
		return Reply{Text: pause.Text, ToolCalls: append([]ToolCallInfo{}, p.ToolCalls...)}, p.ConversationID, true, nil
	}
	items = append(items, toolOutputs...)
	replyText, reasoning, toolCalls, err := a.replyWithToolsStream(ctx, sess, p.UserID, p.ConversationID, items, append(p.ToolCalls, infos...), emit)
	if err != nil {
		return Reply{}, p.ConversationID, true, err
	}
	return Reply{Text: replyText, Reasoning: reasoning, ToolCalls: toolCalls}, p.ConversationID, true, nil
}

func (a *Agent) prunePendingLocked(now time.Time) {
	if a.confirm == nil {
		return
	}
	for tok, p := range a.pendingRuns {
		if p == nil || now.Sub(p.CreatedAt) > a.confirm.ttl {
			delete(a.pendingRuns, tok)
		}
	}
}

func injectConfirmToken(args string, token string) string {
	token = strings.TrimSpace(token)
	if token == "" {
		return args
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(args)), &obj); err != nil || obj == nil {
		return args
	}
	obj["confirm_token"] = token
	b, err := json.Marshal(obj)
	if err != nil {
		return args
	}
	return string(b)
}
