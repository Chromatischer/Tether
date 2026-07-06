package store_test

import (
	"testing"

	"tether/internal/store"
)

func TestMessageReasoning_RoundTripAndModelScope(t *testing.T) {
	d := openTestDB(t)
	u, err := store.CreateUser(d, "alice", "pw")
	if err != nil {
		t.Fatal(err)
	}
	conv, err := store.CreateConversation(d, u.ID, "")
	if err != nil {
		t.Fatal(err)
	}

	blocks := []store.ReasoningBlock{{ItemID: "rs_1", EncryptedContent: "signed-blob"}}
	msgID, err := store.AddAssistantMessageWithReasoning(d, conv.ID, "the answer", "anthropic/claude-x", blocks)
	if err != nil {
		t.Fatal(err)
	}
	if msgID == 0 {
		t.Fatal("expected a message id")
	}

	// Matching model returns the block.
	got, err := store.GetMessageReasoningForConversation(d, conv.ID, "anthropic/claude-x")
	if err != nil {
		t.Fatal(err)
	}
	if len(got[msgID]) != 1 || got[msgID][0].EncryptedContent != "signed-blob" {
		t.Fatalf("expected stored block, got %+v", got)
	}

	// Different model must not return a block signed by another model.
	got2, err := store.GetMessageReasoningForConversation(d, conv.ID, "openai/o-x")
	if err != nil {
		t.Fatal(err)
	}
	if len(got2) != 0 {
		t.Fatalf("expected no blocks for different model, got %+v", got2)
	}
}

func TestSetMessageReasoning_SkipsEmptyAndBlankBlocks(t *testing.T) {
	d := openTestDB(t)
	u, _ := store.CreateUser(d, "bob", "pw")
	conv, _ := store.CreateConversation(d, u.ID, "")
	msgID, err := store.AddMessageID(d, conv.ID, "assistant", "hi")
	if err != nil {
		t.Fatal(err)
	}

	// No blocks → no row.
	if err := store.SetMessageReasoning(d, msgID, conv.ID, "m", nil); err != nil {
		t.Fatal(err)
	}
	// Blank encrypted content is filtered out → still no row.
	if err := store.SetMessageReasoning(d, msgID, conv.ID, "m", []store.ReasoningBlock{{ItemID: "x"}}); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetMessageReasoningForConversation(d, conv.ID, "m")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("expected no stored reasoning, got %+v", got)
	}
}
