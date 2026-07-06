package store

import (
	"database/sql"
	"encoding/json"
	"strings"
)

// ReasoningBlock is a single signed/encrypted reasoning item as returned by the
// provider (via OpenRouter's Responses API). EncryptedContent carries the
// opaque, signed payload that must be replayed verbatim; ItemID is the original
// reasoning item id, replayed best-effort.
type ReasoningBlock struct {
	ItemID           string `json:"id,omitempty"`
	EncryptedContent string `json:"ec"`
}

// SetMessageReasoning persists the signed reasoning behind an assistant message.
// model pins the producing model so the blocks are only replayed back to a model
// that can verify them. A nil/empty blocks slice is a no-op.
func SetMessageReasoning(db *sql.DB, messageID, conversationID int64, model string, blocks []ReasoningBlock) error {
	if db == nil || messageID == 0 {
		return nil
	}
	kept := make([]ReasoningBlock, 0, len(blocks))
	for _, b := range blocks {
		if strings.TrimSpace(b.EncryptedContent) == "" {
			continue
		}
		kept = append(kept, b)
	}
	if len(kept) == 0 {
		return nil
	}
	payload, err := json.Marshal(kept)
	if err != nil {
		return err
	}
	_, err = db.Exec(
		`INSERT OR REPLACE INTO message_reasoning(message_id, conversation_id, model, blocks_json) VALUES (?,?,?,?)`,
		messageID, conversationID, strings.TrimSpace(model), string(payload),
	)
	return err
}

// AddAssistantMessageWithReasoning persists an assistant message and, when
// present, its signed reasoning blocks keyed to the new message id. Returns the
// message id.
func AddAssistantMessageWithReasoning(db *sql.DB, conversationID int64, content, model string, blocks []ReasoningBlock) (int64, error) {
	id, err := AddMessageID(db, conversationID, "assistant", content)
	if err != nil {
		return 0, err
	}
	if len(blocks) > 0 {
		_ = SetMessageReasoning(db, id, conversationID, model, blocks)
	}
	return id, nil
}

// GetMessageReasoningForConversation returns the stored reasoning blocks for a
// conversation, keyed by message id. Only rows whose stored model matches the
// supplied model are returned — a block signed by a different model would fail
// signature validation if replayed.
func GetMessageReasoningForConversation(db *sql.DB, conversationID int64, model string) (map[int64][]ReasoningBlock, error) {
	if db == nil || conversationID == 0 {
		return nil, nil
	}
	rows, err := db.Query(
		`SELECT message_id, blocks_json FROM message_reasoning WHERE conversation_id=? AND model=?`,
		conversationID, strings.TrimSpace(model),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[int64][]ReasoningBlock{}
	for rows.Next() {
		var msgID int64
		var raw string
		if err := rows.Scan(&msgID, &raw); err != nil {
			return nil, err
		}
		var blocks []ReasoningBlock
		if err := json.Unmarshal([]byte(raw), &blocks); err != nil {
			// Skip corrupt rows rather than failing the whole turn.
			continue
		}
		if len(blocks) > 0 {
			out[msgID] = blocks
		}
	}
	return out, rows.Err()
}
