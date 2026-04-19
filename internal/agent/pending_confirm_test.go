package agent

import (
	"testing"
	"time"
)

func TestHasPendingConfirmationToken(t *testing.T) {
	ag := &Agent{confirm: newConfirmManager(), pendingRuns: map[string]*pendingConfirmation{}}
	ag.pendingRuns["tok"] = &pendingConfirmation{UserID: 1, ConversationID: 2, Token: "tok", CreatedAt: time.Now()}

	if !ag.HasPendingConfirmationToken(1, "tok") {
		t.Fatalf("expected token to be pending")
	}
	if ag.HasPendingConfirmationToken(2, "tok") {
		t.Fatalf("expected token to be rejected for wrong user")
	}
	if ag.HasPendingConfirmationToken(1, "") {
		t.Fatalf("expected empty token to be false")
	}
}

func TestHasPendingConfirmationToken_PrunesExpired(t *testing.T) {
	ag := &Agent{confirm: newConfirmManager(), pendingRuns: map[string]*pendingConfirmation{}}
	ag.confirm.ttl = 10 * time.Millisecond
	ag.pendingRuns["tok"] = &pendingConfirmation{UserID: 1, ConversationID: 2, Token: "tok", CreatedAt: time.Now().Add(-time.Second)}

	if ag.HasPendingConfirmationToken(1, "tok") {
		t.Fatalf("expected expired pending token to be pruned")
	}
}
