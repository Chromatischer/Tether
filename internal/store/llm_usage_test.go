package store_test

import (
	"testing"

	"tether/internal/store"
	"tether/internal/testutil"
)

func TestLatestConversationLLMUsage(t *testing.T) {
	db := testutil.OpenTestDB(t)
	userID := int64(7)
	payloads := []string{
		`{"model":"a","conversation_id":11,"input_tokens":10,"output_tokens":5,"total_tokens":15,"cost":0.01}`,
		`{"model":"b","conversation_id":12,"input_tokens":20,"output_tokens":10,"total_tokens":30,"cost":0.02}`,
		`{"model":"a","conversation_id":11,"input_tokens":30,"output_tokens":7,"total_tokens":37,"cost":0.03}`,
	}
	for _, p := range payloads {
		if err := store.AddAuditEvent(db, &userID, "llm_usage", p); err != nil {
			t.Fatal(err)
		}
	}

	got, ok, err := store.LatestConversationLLMUsage(db, userID, 11)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected llm usage")
	}
	if got.Model != "a" || got.InputTokens != 30 || got.SumInputTokens != 40 || got.SumTotalTokens != 52 {
		t.Fatalf("unexpected usage %+v", got)
	}
}
