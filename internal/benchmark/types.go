package benchmark

import "time"

type ProgressEvent struct {
	Stage   string
	RunID   string
	ModelID string
	TaskID  string
	JudgeID string
	Message string
	Err     string
}

type RunResult struct {
	RunID          string          `json:"run_id"`
	Name           string          `json:"name"`
	Description    string          `json:"description,omitempty"`
	StartedAt      time.Time       `json:"started_at"`
	CompletedAt    time.Time       `json:"completed_at"`
	Duration       time.Duration   `json:"duration"`
	ConfigPath     string          `json:"config_path,omitempty"`
	ResultsPath    string          `json:"results_path,omitempty"`
	Attempts       []AttemptResult `json:"attempts"`
	TaskSummaries  []TaskSummary   `json:"task_summaries"`
	ModelSummaries []ModelSummary  `json:"model_summaries"`
}

type AttemptResult struct {
	ModelID           string           `json:"model_id"`
	ModelLabel        string           `json:"model_label"`
	ModelName         string           `json:"model_name"`
	TaskID            string           `json:"task_id"`
	TaskTitle         string           `json:"task_title"`
	Category          string           `json:"category"`
	StartedAt         time.Time        `json:"started_at"`
	CompletedAt       time.Time        `json:"completed_at"`
	Duration          time.Duration    `json:"duration"`
	FirstTokenLatency time.Duration    `json:"first_token_latency"`
	Completed         bool             `json:"completed"`
	Error             string           `json:"error,omitempty"`
	FinalOutput       string           `json:"final_output"`
	Transcript        []TranscriptTurn `json:"transcript,omitempty"`
	PrefillMessages   int              `json:"prefill_messages,omitempty"`
	PrefillTokens     int              `json:"prefill_tokens,omitempty"`
	ToolCalls         []string         `json:"tool_calls,omitempty"`
	TotalToolCalls    int              `json:"total_tool_calls"`
	InputTokens       int              `json:"input_tokens"`
	OutputTokens      int              `json:"output_tokens"`
	TotalTokens       int              `json:"total_tokens"`
	TotalCost         float64          `json:"total_cost"`
	ContextPct        float64          `json:"context_pct"`
	CompletionScore   float64          `json:"completion_score"`
	DurationScore     float64          `json:"duration_score"`
	LatencyScore      float64          `json:"latency_score"`
	TokenScore        float64          `json:"token_score"`
	CostScore         float64          `json:"cost_score"`
	EfficiencyScore   float64          `json:"efficiency_score"`
	ObjectiveScore    float64          `json:"objective_score"`
	SubjectiveScore   float64          `json:"subjective_score"`
	OverallScore      float64          `json:"overall_score"`
	JudgeRankPoints   float64          `json:"judge_rank_points"`
	JudgeRubric       RubricScore      `json:"judge_rubric"`
	JudgeNotes        []JudgeNote      `json:"judge_notes,omitempty"`
}

type TranscriptTurn struct {
	Prompt string `json:"prompt"`
	Output string `json:"output"`
}

type RubricScore struct {
	Correctness  float64 `json:"correctness"`
	Completeness float64 `json:"completeness"`
	Usefulness   float64 `json:"usefulness"`
	Style        float64 `json:"style"`
}

type JudgeNote struct {
	JudgeID string `json:"judge_id"`
	Round   int    `json:"round"`
	Group   int    `json:"group"`
	Note    string `json:"note"`
}

type TaskSummary struct {
	TaskID        string          `json:"task_id"`
	TaskTitle     string          `json:"task_title"`
	Category      string          `json:"category"`
	WinnerModelID string          `json:"winner_model_id,omitempty"`
	Attempts      []AttemptResult `json:"attempts"`
}

type ModelSummary struct {
	ModelID           string        `json:"model_id"`
	ModelLabel        string        `json:"model_label"`
	ModelName         string        `json:"model_name"`
	TasksCompleted    int           `json:"tasks_completed"`
	TasksTotal        int           `json:"tasks_total"`
	AverageObjective  float64       `json:"average_objective"`
	AverageSubjective float64       `json:"average_subjective"`
	AverageOverall    float64       `json:"average_overall"`
	TotalDuration     time.Duration `json:"total_duration"`
	TotalTokens       int           `json:"total_tokens"`
	TotalCost         float64       `json:"total_cost"`
}

type judgeDecision struct {
	Winner  string                 `json:"winner"`
	Ranking []string               `json:"ranking"`
	Scores  map[string]RubricScore `json:"scores"`
	Summary string                 `json:"summary"`
}
