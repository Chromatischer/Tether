package benchmark

import "testing"

func TestApplyScoresRanksBetterAttemptHigher(t *testing.T) {
	r := &Runner{bench: &Config{Scoring: ScoringConfig{
		ObjectiveWeight:   0.35,
		SubjectiveWeight:  0.65,
		CompletionWeight:  0.2,
		DurationWeight:    0.25,
		LatencyWeight:     0.1,
		TokenWeight:       0.2,
		CostWeight:        0.15,
		EfficiencyWeight:  0.1,
		JudgeRankWeight:   0.45,
		JudgeRubricWeight: 0.55,
		Rubric: RubricWeights{
			Correctness:  0.4,
			Completeness: 0.2,
			Usefulness:   0.25,
			Style:        0.15,
		},
	}}}
	run := &RunResult{
		Attempts: []AttemptResult{
			{
				ModelID:           "best",
				TaskID:            "task",
				TaskTitle:         "Task",
				Category:          "chat",
				Completed:         true,
				Duration:          10,
				FirstTokenLatency: 2,
				TotalTokens:       100,
				TotalCost:         0.01,
				TotalToolCalls:    1,
				JudgeRankPoints:   10,
				JudgeRubric:       RubricScore{Correctness: 9, Completeness: 8, Usefulness: 8, Style: 8},
			},
			{
				ModelID:           "worse",
				TaskID:            "task",
				TaskTitle:         "Task",
				Category:          "chat",
				Completed:         true,
				Duration:          20,
				FirstTokenLatency: 5,
				TotalTokens:       250,
				TotalCost:         0.04,
				TotalToolCalls:    4,
				JudgeRankPoints:   2,
				JudgeRubric:       RubricScore{Correctness: 6, Completeness: 6, Usefulness: 5, Style: 5},
			},
		},
	}
	r.applyScores(run)
	if run.Attempts[0].OverallScore <= run.Attempts[1].OverallScore {
		t.Fatalf("expected first attempt to outrank second: %.3f <= %.3f", run.Attempts[0].OverallScore, run.Attempts[1].OverallScore)
	}
}
