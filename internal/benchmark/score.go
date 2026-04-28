package benchmark

import (
	"sort"
	"time"
)

func (r *Runner) applyScores(run *RunResult) {
	byTask := map[string][]*AttemptResult{}
	for i := range run.Attempts {
		attempt := &run.Attempts[i]
		byTask[attempt.TaskID] = append(byTask[attempt.TaskID], attempt)
	}
	for _, attempts := range byTask {
		applyObjectiveScores(attempts, r.bench.Scoring)
		applySubjectiveScores(attempts, r.bench.Scoring)
		for _, attempt := range attempts {
			attempt.OverallScore = attempt.ObjectiveScore*r.bench.Scoring.ObjectiveWeight + attempt.SubjectiveScore*r.bench.Scoring.SubjectiveWeight
		}
	}
}

func applyObjectiveScores(attempts []*AttemptResult, scoring ScoringConfig) {
	durationVals := make([]float64, 0, len(attempts))
	latencyVals := make([]float64, 0, len(attempts))
	tokenVals := make([]float64, 0, len(attempts))
	costVals := make([]float64, 0, len(attempts))
	effVals := make([]float64, 0, len(attempts))
	for _, attempt := range attempts {
		if !attempt.Completed {
			continue
		}
		durationVals = append(durationVals, durationToFloat(attempt.Duration))
		latencyVals = append(latencyVals, durationToFloat(attempt.FirstTokenLatency))
		tokenVals = append(tokenVals, float64(attempt.TotalTokens))
		costVals = append(costVals, attempt.TotalCost)
		effVals = append(effVals, float64(attempt.TotalToolCalls))
	}
	for _, attempt := range attempts {
		completion := 0.0
		if attempt.Completed {
			completion = 1
		}
		durationScore := inverseNormalized(durationToFloat(attempt.Duration), durationVals, attempt.Completed)
		latencyScore := inverseNormalized(durationToFloat(attempt.FirstTokenLatency), latencyVals, attempt.Completed)
		tokenScore := inverseNormalized(float64(attempt.TotalTokens), tokenVals, attempt.Completed)
		costScore := inverseNormalized(attempt.TotalCost, costVals, attempt.Completed)
		efficiencyScore := inverseNormalized(float64(attempt.TotalToolCalls), effVals, attempt.Completed)
		attempt.CompletionScore = completion
		attempt.DurationScore = durationScore
		attempt.LatencyScore = latencyScore
		attempt.TokenScore = tokenScore
		attempt.CostScore = costScore
		attempt.EfficiencyScore = efficiencyScore
		attempt.ObjectiveScore = completion*scoring.CompletionWeight +
			durationScore*scoring.DurationWeight +
			latencyScore*scoring.LatencyWeight +
			tokenScore*scoring.TokenWeight +
			costScore*scoring.CostWeight +
			efficiencyScore*scoring.EfficiencyWeight
	}
}

func applySubjectiveScores(attempts []*AttemptResult, scoring ScoringConfig) {
	points := make([]float64, 0, len(attempts))
	for _, attempt := range attempts {
		points = append(points, attempt.JudgeRankPoints)
	}
	for _, attempt := range attempts {
		rankScore := normalized(attempt.JudgeRankPoints, points, attempt.Completed)
		rubricScore := weightedRubricScore(attempt.JudgeRubric, scoring.Rubric)
		attempt.SubjectiveScore = rankScore*scoring.JudgeRankWeight + rubricScore*scoring.JudgeRubricWeight
	}
}

func (r *Runner) buildTaskSummaries(attempts []AttemptResult) []TaskSummary {
	byTask := map[string][]AttemptResult{}
	for _, attempt := range attempts {
		byTask[attempt.TaskID] = append(byTask[attempt.TaskID], attempt)
	}
	taskIDs := make([]string, 0, len(byTask))
	for id := range byTask {
		taskIDs = append(taskIDs, id)
	}
	sort.Strings(taskIDs)
	out := make([]TaskSummary, 0, len(taskIDs))
	for _, taskID := range taskIDs {
		list := append([]AttemptResult(nil), byTask[taskID]...)
		sort.Slice(list, func(i, j int) bool {
			if list[i].OverallScore == list[j].OverallScore {
				return list[i].ModelID < list[j].ModelID
			}
			return list[i].OverallScore > list[j].OverallScore
		})
		summary := TaskSummary{
			TaskID:    taskID,
			TaskTitle: list[0].TaskTitle,
			Category:  list[0].Category,
			Attempts:  list,
		}
		if len(list) > 0 {
			summary.WinnerModelID = list[0].ModelID
		}
		out = append(out, summary)
	}
	return out
}

func (r *Runner) buildModelSummaries(attempts []AttemptResult) []ModelSummary {
	byModel := map[string][]AttemptResult{}
	for _, attempt := range attempts {
		byModel[attempt.ModelID] = append(byModel[attempt.ModelID], attempt)
	}
	modelIDs := make([]string, 0, len(byModel))
	for id := range byModel {
		modelIDs = append(modelIDs, id)
	}
	sort.Strings(modelIDs)
	out := make([]ModelSummary, 0, len(modelIDs))
	for _, modelID := range modelIDs {
		list := byModel[modelID]
		summary := ModelSummary{
			ModelID:    modelID,
			ModelLabel: list[0].ModelLabel,
			ModelName:  list[0].ModelName,
			TasksTotal: len(list),
		}
		for _, attempt := range list {
			if attempt.Completed {
				summary.TasksCompleted++
			}
			summary.AverageObjective += attempt.ObjectiveScore
			summary.AverageSubjective += attempt.SubjectiveScore
			summary.AverageOverall += attempt.OverallScore
			summary.TotalDuration += attempt.Duration
			summary.TotalTokens += attempt.TotalTokens
			summary.TotalCost += attempt.TotalCost
		}
		div := float64(len(list))
		if div > 0 {
			summary.AverageObjective /= div
			summary.AverageSubjective /= div
			summary.AverageOverall /= div
		}
		out = append(out, summary)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].AverageOverall == out[j].AverageOverall {
			return out[i].ModelID < out[j].ModelID
		}
		return out[i].AverageOverall > out[j].AverageOverall
	})
	return out
}

func weightedRubricScore(score RubricScore, weights RubricWeights) float64 {
	totalWeight := weights.Correctness + weights.Completeness + weights.Usefulness + weights.Style
	if totalWeight <= 0 {
		return 0
	}
	total := score.Correctness*weights.Correctness + score.Completeness*weights.Completeness + score.Usefulness*weights.Usefulness + score.Style*weights.Style
	return (total / totalWeight) / 10.0
}

func durationToFloat(d time.Duration) float64 {
	return float64(d.Milliseconds())
}

func inverseNormalized(value float64, values []float64, ok bool) float64 {
	if !ok {
		return 0
	}
	if len(values) == 0 {
		return 1
	}
	min, max := minMax(values)
	if max <= min {
		return 1
	}
	return 1 - ((value - min) / (max - min))
}

func normalized(value float64, values []float64, ok bool) float64 {
	if !ok {
		return 0
	}
	if len(values) == 0 {
		return 0
	}
	min, max := minMax(values)
	if max <= min {
		if value == 0 {
			return 0
		}
		return 1
	}
	return (value - min) / (max - min)
}

func minMax(values []float64) (float64, float64) {
	min := values[0]
	max := values[0]
	for _, v := range values[1:] {
		if v < min {
			min = v
		}
		if v > max {
			max = v
		}
	}
	return min, max
}
