// Package chatcmd holds the presentation-agnostic logic for the slash commands
// that behave identically across every frontend (the SSH/local TUI and the
// Discord gateway). Each function performs the store/secret/tool operation and
// returns the exact response string both frontends display. Argument-count
// validation, usage text, transcript echoing and proactive-trigger side effects
// stay in the callers, since those differ per frontend.
package chatcmd

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"

	"tether/internal/secrets"
	"tether/internal/store"
	"tether/internal/subagents"
	"tether/internal/tools"
)

// ---- /memory ----

func MemoryList(db *sql.DB, userID int64, kind string) string {
	items, err := store.ListMemoryItems(db, userID, kind, 100)
	if err != nil {
		return "failed to list memory: " + err.Error()
	}
	if len(items) == 0 {
		return "no memory items"
	}
	var b strings.Builder
	b.WriteString("Memory:\n")
	for _, it := range items {
		b.WriteString("- ")
		b.WriteString(fmt.Sprintf("%d", it.ID))
		b.WriteString(" [")
		b.WriteString(it.Kind)
		b.WriteString("] ")
		b.WriteString(it.Content)
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

func MemoryAdd(db *sql.DB, userID int64, kind, content string) string {
	id, err := store.AddMemoryItem(db, userID, kind, content)
	if err != nil {
		return "failed to add memory: " + err.Error()
	}
	return "memory added (id " + fmt.Sprintf("%d", id) + ")"
}

func MemoryUpdate(db *sql.DB, userID int64, idStr, content string) string {
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		return "invalid id"
	}
	if err := store.UpdateMemoryItem(db, userID, id, content); err != nil {
		return "failed to update memory: " + err.Error()
	}
	return "memory updated"
}

func MemoryDelete(db *sql.DB, userID int64, idStr string) string {
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		return "invalid id"
	}
	if err := store.DeleteMemoryItem(db, userID, id); err != nil {
		return "failed to delete memory: " + err.Error()
	}
	return "memory deleted"
}

// ---- /task (tasks are memory items with kind "task") ----

func TaskList(db *sql.DB, userID int64) string {
	items, err := store.ListMemoryItems(db, userID, "task", 100)
	if err != nil {
		return "failed: " + err.Error()
	}
	if len(items) == 0 {
		return "no tasks"
	}
	var b strings.Builder
	b.WriteString("Tasks:\n")
	for _, it := range items {
		b.WriteString("- ")
		b.WriteString(fmt.Sprintf("%d", it.ID))
		b.WriteString(": ")
		b.WriteString(it.Content)
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

// TaskAdd/TaskUpdate/TaskDone return the response string and whether the
// underlying mutation succeeded. Callers use the bool to decide whether to fire
// the proactive "task changed" event, which must not run on a failed op.

func TaskAdd(db *sql.DB, userID int64, content string) (string, bool) {
	id, err := store.AddMemoryItem(db, userID, "task", content)
	if err != nil {
		return "failed: " + err.Error(), false
	}
	return "task added (id " + fmt.Sprintf("%d", id) + ")", true
}

func TaskUpdate(db *sql.DB, userID int64, idStr, content string) (string, bool) {
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		return "invalid id", false
	}
	if err := store.UpdateMemoryItem(db, userID, id, content); err != nil {
		return "failed: " + err.Error(), false
	}
	return "task updated", true
}

func TaskDone(db *sql.DB, userID int64, idStr string) (string, bool) {
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		return "invalid id", false
	}
	if err := store.DeleteMemoryItem(db, userID, id); err != nil {
		return "failed: " + err.Error(), false
	}
	return "task marked done", true
}

// ---- /secret ----

// OpenSecretStore builds the per-user secret store. On failure it returns a nil
// store and the response string explaining how to configure secrets.
func OpenSecretStore(db *sql.DB, masterKey string, ttlHours int) (*secrets.Store, string) {
	s, err := secrets.NewStore(db, masterKey, time.Duration(ttlHours)*time.Hour)
	if err != nil {
		return nil, "secrets unavailable: " + err.Error() + " (set TETHER_MASTER_KEY; generate with: go run ./cmd/tether-keygen)"
	}
	return s, ""
}

func SecretAdd(s *secrets.Store, userID int64, label, secret string, ttlHours int) string {
	if err := s.Put(context.Background(), userID, label, secret); err != nil {
		return "failed to store secret: " + err.Error()
	}
	exp := time.Now().Add(time.Duration(ttlHours) * time.Hour).Format(time.RFC3339)
	return "secret stored as '" + label + "' (expires ~" + exp + ")"
}

func SecretList(s *secrets.Store, userID int64) string {
	items, err := s.List(context.Background(), userID)
	if err != nil {
		return "failed to list secrets: " + err.Error()
	}
	if len(items) == 0 {
		return "no secrets set"
	}
	var b strings.Builder
	b.WriteString("Secrets (labels only):\n")
	for _, it := range items {
		b.WriteString("- ")
		b.WriteString(it.Label)
		b.WriteString(" (expires ")
		b.WriteString(it.ExpiresAt.Format(time.RFC3339))
		b.WriteString(")\n")
	}
	return strings.TrimSpace(b.String())
}

func SecretDelete(s *secrets.Store, userID int64, label string) string {
	if err := s.Delete(context.Background(), userID, label); err != nil {
		return "failed to delete secret: " + err.Error()
	}
	return "deleted secret '" + label + "'"
}

func SecretClear(s *secrets.Store, userID int64) string {
	if err := s.Clear(context.Background(), userID); err != nil {
		return "failed to clear secrets: " + err.Error()
	}
	return "cleared all secrets"
}

// ---- /tools ----

func ToolsList(reg *tools.Registry) string { return formatToolInfos(reg.List()) }
func ToolsSearch(reg *tools.Registry, query string) string {
	return formatToolInfos(reg.Search(query))
}

func ToolsDescribe(reg *tools.Registry, name string) string {
	spec, ok := reg.Get(strings.TrimSpace(name))
	if !ok {
		return "unknown tool: " + strings.TrimSpace(name)
	}
	return strings.TrimSpace(tools.RenderToolMarkdown(spec))
}

func ToolsCategories(reg *tools.Registry) string {
	var b strings.Builder
	b.WriteString("Tool categories (tools are enabled by category):\n")
	for _, c := range reg.Categories() {
		b.WriteString("- ")
		b.WriteString(c.Name)
		switch {
		case c.AlwaysOn:
			b.WriteString(" (always on)")
		case c.DefaultOn:
			b.WriteString(" (on by default)")
		default:
			b.WriteString(" (off by default)")
		}
		if c.Description != "" {
			b.WriteString(" — ")
			b.WriteString(c.Description)
		}
		if len(c.Tools) > 0 {
			b.WriteString("\n    ")
			b.WriteString(strings.Join(c.Tools, ", "))
		}
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

func formatToolInfos(infos []tools.ToolInfo) string {
	var b strings.Builder
	b.WriteString("Tools:\n")
	for _, t := range infos {
		b.WriteString("- ")
		b.WriteString(t.Name)
		if t.Description != "" {
			b.WriteString(" — ")
			b.WriteString(t.Description)
		}
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

// ---- /signal ----

func SignalLink(db *sql.DB, userID int64, accountNumber string) string {
	code, err := store.CreateSignalLinkCode(db, userID, 10*time.Minute)
	if err != nil {
		return "failed to create link code: " + err.Error()
	}
	acct := strings.TrimSpace(accountNumber)
	if acct == "" {
		acct = "<signal account not configured>"
	}
	return "Signal link code: " + code + "\nSend this code from your phone number to the Tether Signal account: " + acct + "\n(Code expires in ~10 minutes.)"
}

func SignalStatus(db *sql.DB, userID int64) string {
	n, ok, err := store.GetSignalNumber(db, userID)
	if err != nil {
		return "failed to get signal status: " + err.Error()
	}
	if !ok {
		return "Signal: not linked"
	}
	return "Signal linked: " + n
}

func SignalUnlink(db *sql.DB, userID int64) string {
	if err := store.UnlinkSignalNumber(db, userID); err != nil {
		return "failed to unlink: " + err.Error()
	}
	return "Signal unlinked"
}

// ---- /subagent ----

func SubagentSpawn(mgr *subagents.Manager, userID int64, prompt string) string {
	run := mgr.Spawn(userID, subagents.RunRequest{Prompt: prompt})
	return "spawned subagent: " + run.ID + " (status: " + string(run.Status) + ")"
}

func SubagentStatus(mgr *subagents.Manager, userID int64, id string) string {
	run, ok := mgr.GetForUser(userID, id)
	if !ok {
		return "subagent not found: " + id
	}
	resp := "subagent " + run.ID + ": " + string(run.Status)
	if run.Err != "" {
		resp += "\nerror: " + run.Err
	}
	if run.Result != "" {
		resp += "\nresult:\n" + run.Result
	}
	return resp
}
