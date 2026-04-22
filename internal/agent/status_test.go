package agent

import (
	"testing"

	"tether/internal/config"
	"tether/internal/store"
	"tether/internal/testutil"
)

func TestSessionStatus_FallsBackToAuditUsage(t *testing.T) {
	db := testutil.OpenTestDB(t)
	cfg := &config.Config{}
	cfg.Paths.DataDir = t.TempDir()
	ag := newAgent(cfg, db)

	userID := int64(7)
	convID := int64(11)
	if err := store.AddAuditEvent(db, &userID, "llm_usage", `{"model":"openai/gpt-4o","conversation_id":11,"input_tokens":50,"output_tokens":10,"total_tokens":60,"cost":0.01}`); err != nil {
		t.Fatal(err)
	}

	st := ag.SessionStatus(userID, convID)
	if st.UsageSource != "audit" {
		t.Fatalf("expected audit usage source, got %+v", st)
	}
	if st.TotalTokens != 60 || st.LastInputTokens != 50 || st.LastModel != "openai/gpt-4o" {
		t.Fatalf("unexpected status %+v", st)
	}
}
