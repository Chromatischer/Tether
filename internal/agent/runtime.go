package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"tether/internal/agent/toolset"
	"tether/internal/cache"
	"tether/internal/config"
	"tether/internal/llm/openrouter"
	"tether/internal/secrets"
	"tether/internal/store"
	"tether/internal/subagents"
	"tether/internal/tools"
	"tether/internal/userspace"
)

type subagentStore struct {
	mgr *subagents.Manager
}

type auditedSecrets struct {
	store *secrets.Store
	db    *sql.DB
}

type auditedConfirmer struct {
	mgr *confirmManager
	db  *sql.DB
}

func (a auditedSecrets) Get(ctx context.Context, userID int64, label string) (string, bool, error) {
	payload, _ := json.Marshal(map[string]any{"label": label})
	_ = store.AddAuditEvent(a.db, &userID, "secret_access", string(payload))
	return a.store.Get(ctx, userID, label)
}

func (c auditedConfirmer) Request(userID int64, scope string, reason string) string {
	tok := c.mgr.Request(userID, scope, reason)
	if c.db != nil {
		payload, _ := json.Marshal(map[string]any{
			"token":  tok,
			"scope":  scope,
			"reason": truncateAuditString(reason, 300),
		})
		_ = store.AddAuditEvent(c.db, &userID, "confirm_request", string(payload))
	}
	return tok
}

func (c auditedConfirmer) Consume(userID int64, token string, scope string) bool {
	ok := c.mgr.Consume(userID, token, scope)
	if c.db != nil {
		payload, _ := json.Marshal(map[string]any{
			"token": token,
			"scope": scope,
			"ok":    ok,
		})
		_ = store.AddAuditEvent(c.db, &userID, "confirm_consume", string(payload))
	}
	return ok
}

func (s subagentStore) Spawn(userID int64, prompt string) (id string) {
	return s.mgr.Spawn(userID, prompt).ID
}

func (s subagentStore) Status(id string) (status any, ok bool) {
	r, ok := s.mgr.Get(id)
	if !ok {
		return nil, false
	}
	return r, true
}

func truncateAuditString(s string, max int) string {
	s = strings.TrimSpace(s)
	if max <= 0 {
		max = 200
	}
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

// newAgent initializes the agent runtime fields.
func newAgent(cfg *config.Config, db *sql.DB) *Agent {
	llm := openrouter.New(cfg.OpenRouter.BaseURL, cfg.OpenRouter.APIKey, "Tether")
	a := &Agent{
		cfg:         cfg,
		db:          db,
		llm:         llm,
		cache:       cache.NewLLMCache(db, 14*24*time.Hour),
		registry:    tools.NewRegistry(),
		sessions:    map[int64]*toolset.Session{},
		confirm:     newConfirmManager(),
		pendingRuns: map[string]*pendingConfirmation{},
	}

	// Optional secrets store (used by tools via secret references)
	if strings.TrimSpace(cfg.Secrets.MasterKey) != "" {
		st, err := secrets.NewStore(db, cfg.Secrets.MasterKey, time.Duration(cfg.Secrets.TTLHours)*time.Hour)
		if err == nil {
			a.secrets = st
		}
	}

	a.subMgr = subagents.NewManager(agentSubagentRunner{ag: a})
	a.subStore = subagentStore{mgr: a.subMgr}

	// Tool implementations + canonical specs.
	a.toolImpl = toolset.DefaultTools()
	for _, impl := range a.toolImpl {
		a.registry.Register(impl.Spec())
	}

	return a
}

func (a *Agent) sessionFor(userID, convID int64) *toolset.Session {
	a.mu.Lock()
	defer a.mu.Unlock()
	s := a.sessions[convID]
	if s == nil {
		s = toolset.NewSession(a.registry)
		a.sessions[convID] = s
	}
	s.UserID = userID
	s.ConversationID = convID
	s.IsSubagent = false
	s.Dirs = userspace.ForUser(a.cfg.Paths.DataDir, userID)
	s.DB = a.db
	s.Subagents = a.subStore
	s.Confirm = auditedConfirmer{mgr: a.confirm, db: a.db}
	s.LLM = a
	if a.secrets != nil {
		s.Secrets = auditedSecrets{store: a.secrets, db: a.db}
	}
	return s
}

func cloneSession(s *toolset.Session) *toolset.Session {
	if s == nil {
		return nil
	}
	cp := *s
	if s.Active != nil {
		cp.Active = make(map[string]bool, len(s.Active))
		for k, v := range s.Active {
			cp.Active[k] = v
		}
	}
	cp.InvokedSkills = append([]toolset.InvokedSkill(nil), s.InvokedSkills...)
	return &cp
}

func (a *Agent) forkSessionFor(userID, convID int64) *toolset.Session {
	return cloneSession(a.sessionFor(userID, convID))
}

func (a *Agent) mergeSessionFor(convID int64, s *toolset.Session) {
	if s == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()

	base := a.sessions[convID]
	if base == nil {
		a.sessions[convID] = cloneSession(s)
		return
	}
	if base.Active == nil {
		base.Active = map[string]bool{}
	}
	for name, enabled := range s.Active {
		if enabled {
			base.Active[name] = true
		}
	}
	for _, inv := range s.InvokedSkills {
		base.AddInvokedSkill(inv.Name, inv.Content)
	}
}

func (a *Agent) ResetConversationSession(convID int64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.sessions, convID)
}
