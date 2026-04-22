package store

import (
	"database/sql"
	"encoding/json"
	"time"
)

type ConversationLLMUsage struct {
	LastSeenAt      time.Time
	Model           string
	InputTokens     int
	OutputTokens    int
	TotalTokens     int
	Cost            float64
	SumInputTokens  int
	SumOutputTokens int
	SumTotalTokens  int
	SumCost         float64
}

func LatestConversationLLMUsage(db *sql.DB, userID, conversationID int64) (ConversationLLMUsage, bool, error) {
	rows, err := db.Query(`SELECT payload_json, created_at FROM audit_events WHERE user_id = ? AND type = 'llm_usage' ORDER BY id ASC`, userID)
	if err != nil {
		return ConversationLLMUsage{}, false, err
	}
	defer rows.Close()

	type payload struct {
		Model          string  `json:"model"`
		ConversationID int64   `json:"conversation_id"`
		InputTokens    int     `json:"input_tokens"`
		OutputTokens   int     `json:"output_tokens"`
		TotalTokens    int     `json:"total_tokens"`
		Cost           float64 `json:"cost"`
	}

	var out ConversationLLMUsage
	found := false
	for rows.Next() {
		var raw string
		var createdAt int64
		if err := rows.Scan(&raw, &createdAt); err != nil {
			return ConversationLLMUsage{}, false, err
		}
		var p payload
		if err := json.Unmarshal([]byte(raw), &p); err != nil {
			continue
		}
		if p.ConversationID != conversationID {
			continue
		}
		found = true
		out.LastSeenAt = time.Unix(createdAt, 0).UTC()
		out.Model = p.Model
		out.InputTokens = p.InputTokens
		out.OutputTokens = p.OutputTokens
		out.TotalTokens = p.TotalTokens
		out.Cost = p.Cost
		out.SumInputTokens += p.InputTokens
		out.SumOutputTokens += p.OutputTokens
		out.SumTotalTokens += p.TotalTokens
		out.SumCost += p.Cost
	}
	if err := rows.Err(); err != nil {
		return ConversationLLMUsage{}, false, err
	}
	return out, found, nil
}
