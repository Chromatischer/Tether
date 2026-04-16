package agent

import (
	"encoding/json"
	"testing"
	"time"

	"tether/internal/testutil"
)

func TestConfirmManager_RequestConfirmConsume(t *testing.T) {
	m := newConfirmManager()
	m.ttl = 50 * time.Millisecond

	tok := m.Request(1, "scope", "reason")
	if tok == "" {
		t.Fatalf("expected token")
	}
	if !m.Confirm(1, tok) {
		t.Fatalf("expected confirm ok")
	}
	if !m.Consume(1, tok, "scope") {
		t.Fatalf("expected consume ok")
	}
	if m.Consume(1, tok, "scope") {
		t.Fatalf("expected token to be single-use")
	}
}

func TestConfirmManager_WrongUserOrScopeRejected(t *testing.T) {
	m := newConfirmManager()
	tok := m.Request(1, "scope", "reason")
	if m.Confirm(2, tok) {
		t.Fatalf("expected confirm rejected for wrong user")
	}
	_ = m.Confirm(1, tok)
	if m.Consume(1, tok, "other") {
		t.Fatalf("expected consume rejected for wrong scope")
	}
}

func TestConfirmToken_AuditedAndTruncatesReason(t *testing.T) {
	d := testutil.OpenTestDB(t)

	ag := &Agent{db: d, confirm: newConfirmManager()}
	longReason := make([]byte, 500)
	for i := range longReason {
		longReason[i] = 'a'
	}
	tok := ag.confirm.Request(1, "scope", string(longReason))

	ok := ag.ConfirmToken(1, tok)
	if !ok {
		t.Fatalf("expected ok")
	}

	var payload string
	if err := d.QueryRow(`SELECT payload_json FROM audit_events WHERE type='confirm_confirm' ORDER BY id DESC LIMIT 1`).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(payload), &m); err != nil {
		t.Fatal(err)
	}
	reason, _ := m["reason"].(string)
	max := 300 + len("…")
	if len(reason) > max {
		t.Fatalf("expected truncated reason (<=%d bytes), got len=%d", max, len(reason))
	}
	if m["ok"] != true {
		t.Fatalf("expected ok=true in audit payload")
	}
}
