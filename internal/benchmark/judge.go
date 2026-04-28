package benchmark

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"golang.org/x/sync/errgroup"

	"tether/internal/llm/openrouter"
)

func (r *Runner) applyJudging(ctx context.Context, run *RunResult, emit func(ProgressEvent)) error {
	byTask := map[string][]*AttemptResult{}
	for i := range run.Attempts {
		attempt := &run.Attempts[i]
		if attempt.Completed && strings.TrimSpace(attempt.FinalOutput) != "" {
			byTask[attempt.TaskID] = append(byTask[attempt.TaskID], attempt)
		}
	}
	eg, egCtx := errgroup.WithContext(ctx)
	eg.SetLimit(r.bench.JudgeParallel)
	for _, taskCfg := range r.bench.Tasks {
		taskCfg := taskCfg
		candidates := byTask[taskCfg.ID]
		if len(candidates) < 2 {
			continue
		}
		eg.Go(func() error {
			return r.judgeTask(egCtx, taskCfg, candidates, emit, run.RunID)
		})
	}
	return eg.Wait()
}

func (r *Runner) judgeTask(ctx context.Context, taskCfg TaskConfig, candidates []*AttemptResult, emit func(ProgressEvent), runID string) error {
	round := 1
	pool := append([]*AttemptResult(nil), candidates...)
	for len(pool) > 1 {
		groups := judgeGroups(pool, r.bench.JudgeGroupSize)
		nextRound := make([]*AttemptResult, 0, len(groups))
		for groupIndex, group := range groups {
			progress(emit, ProgressEvent{Stage: "judge_started", RunID: runID, TaskID: taskCfg.ID, Message: fmt.Sprintf("judging round %d group %d", round, groupIndex+1)})
			decision, err := r.evaluateGroup(ctx, taskCfg, group, round, groupIndex+1, emit, runID)
			if err != nil {
				return err
			}
			winner := group[0]
			best := -1.0
			for _, candidate := range group {
				points := candidate.JudgeRankPoints
				rubric := weightedRubricScore(candidate.JudgeRubric, r.bench.Scoring.Rubric)
				if score := points + rubric; score > best {
					best = score
					winner = candidate
				}
			}
			if len(groups) > 1 {
				nextRound = append(nextRound, winner)
			}
			_ = decision
			progress(emit, ProgressEvent{Stage: "judge_finished", RunID: runID, TaskID: taskCfg.ID, Message: fmt.Sprintf("judging round %d group %d finished", round, groupIndex+1)})
		}
		if len(groups) <= 1 {
			break
		}
		pool = nextRound
		round++
	}
	return nil
}

func (r *Runner) evaluateGroup(ctx context.Context, taskCfg TaskConfig, candidates []*AttemptResult, round int, groupIndex int, emit func(ProgressEvent), runID string) (judgeDecision, error) {
	type result struct {
		decision judgeDecision
		judgeID  string
		weight   float64
	}
	results := make([]result, 0, len(r.bench.Judges))
	for _, judgeCfg := range r.bench.Judges {
		judgeCfg := judgeCfg
		progress(emit, ProgressEvent{Stage: "judge_model_started", RunID: runID, TaskID: taskCfg.ID, JudgeID: judgeCfg.ID, Message: "judge model started"})
		decision, err := r.runJudge(ctx, judgeCfg, taskCfg, candidates)
		if err != nil {
			progress(emit, ProgressEvent{Stage: "judge_model_failed", RunID: runID, TaskID: taskCfg.ID, JudgeID: judgeCfg.ID, Err: err.Error(), Message: "judge model failed"})
			for _, candidate := range candidates {
				candidate.JudgeNotes = append(candidate.JudgeNotes, JudgeNote{
					JudgeID: judgeCfg.ID,
					Round:   round,
					Group:   groupIndex,
					Note:    "Judge failed: " + err.Error(),
				})
			}
			continue
		}
		results = append(results, result{decision: decision, judgeID: judgeCfg.ID, weight: judgeCfg.Weight})
		progress(emit, ProgressEvent{Stage: "judge_model_finished", RunID: runID, TaskID: taskCfg.ID, JudgeID: judgeCfg.ID, Message: "judge model finished"})
	}
	if len(results) == 0 {
		return judgeDecision{Summary: "No judge model returned a valid decision."}, nil
	}

	final := judgeDecision{Scores: map[string]RubricScore{}}
	points := map[string]float64{}
	for _, res := range results {
		n := len(res.decision.Ranking)
		for idx, id := range res.decision.Ranking {
			points[id] += float64(n-idx) * res.weight
		}
		for id, score := range res.decision.Scores {
			acc := final.Scores[id]
			acc.Correctness += score.Correctness * res.weight
			acc.Completeness += score.Completeness * res.weight
			acc.Usefulness += score.Usefulness * res.weight
			acc.Style += score.Style * res.weight
			final.Scores[id] = acc
		}
		if strings.TrimSpace(res.decision.Summary) != "" {
			final.Summary = strings.TrimSpace(final.Summary + "\n" + res.decision.Summary)
		}
	}
	totalWeight := 0.0
	for _, judgeCfg := range r.bench.Judges {
		totalWeight += judgeCfg.Weight
	}
	if totalWeight <= 0 {
		totalWeight = 1
	}
	final.Ranking = rankedIDs(points)
	if len(final.Ranking) > 0 {
		final.Winner = final.Ranking[0]
	}
	for _, candidate := range candidates {
		id := candidate.ModelID
		candidate.JudgeRankPoints += points[id]
		if score, ok := final.Scores[id]; ok {
			candidate.JudgeRubric.Correctness += score.Correctness / totalWeight
			candidate.JudgeRubric.Completeness += score.Completeness / totalWeight
			candidate.JudgeRubric.Usefulness += score.Usefulness / totalWeight
			candidate.JudgeRubric.Style += score.Style / totalWeight
		}
		if strings.TrimSpace(final.Summary) != "" {
			candidate.JudgeNotes = append(candidate.JudgeNotes, JudgeNote{
				JudgeID: strings.Join(judgeIDs(r.bench.Judges), ","),
				Round:   round,
				Group:   groupIndex,
				Note:    strings.TrimSpace(final.Summary),
			})
		}
	}
	return final, nil
}

func (r *Runner) runJudge(ctx context.Context, judgeCfg JudgeConfig, taskCfg TaskConfig, candidates []*AttemptResult) (judgeDecision, error) {
	client := openrouter.New(r.cfg.OpenRouter.BaseURL, r.cfg.OpenRouter.APIKey, "Tether Benchmark Judge")
	prompt := buildJudgePrompt(taskCfg, candidates)
	resp, err := client.Responses(ctx, openrouter.ResponsesRequest{
		Model: judgeCfg.Model,
		Input: []openrouter.ResponseItem{
			{Type: "message", Role: "system", Content: []openrouter.ContentPart{{Type: "input_text", Text: judgeSystemPrompt()}}},
			{Type: "message", Role: "user", Content: []openrouter.ContentPart{{Type: "input_text", Text: prompt}}},
		},
		MaxOutputTokens: 2000,
		Temperature:     0,
		ToolChoice:      "none",
		Provider:        providerPrefs(judgeCfg.Provider),
	})
	if err != nil {
		return judgeDecision{}, err
	}
	text := extractJudgeText(resp)
	out, err := parseJudgeDecision(text)
	if err == nil {
		return out, nil
	}
	repaired, repairErr := r.repairJudgeOutput(ctx, client, judgeCfg, text)
	if repairErr != nil {
		return judgeDecision{}, fmt.Errorf("parse judge output: %w", err)
	}
	return repaired, nil
}

func (r *Runner) repairJudgeOutput(ctx context.Context, client *openrouter.Client, judgeCfg JudgeConfig, raw string) (judgeDecision, error) {
	resp, err := client.Responses(ctx, openrouter.ResponsesRequest{
		Model: judgeCfg.Model,
		Input: []openrouter.ResponseItem{
			{Type: "message", Role: "system", Content: []openrouter.ContentPart{{Type: "input_text", Text: "Rewrite the provided content into strict JSON only. Output exactly one JSON object and nothing else."}}},
			{Type: "message", Role: "user", Content: []openrouter.ContentPart{{Type: "input_text", Text: "Convert this judge output into valid JSON with keys winner, ranking, scores, summary.\n\n" + raw}}},
		},
		MaxOutputTokens: 1200,
		Temperature:     0,
		ToolChoice:      "none",
		Provider:        providerPrefs(judgeCfg.Provider),
	})
	if err != nil {
		return judgeDecision{}, err
	}
	return parseJudgeDecision(extractJudgeText(resp))
}

func parseJudgeDecision(text string) (judgeDecision, error) {
	var out judgeDecision
	if err := json.Unmarshal([]byte(extractJudgeJSON(text)), &out); err != nil {
		return judgeDecision{}, err
	}
	if len(out.Ranking) == 0 {
		return judgeDecision{}, fmt.Errorf("judge returned empty ranking")
	}
	return out, nil
}

func providerPrefs(cfg ProviderConfig) *openrouter.ProviderPreferences {
	if cfg.AllowFallbacks == nil && len(cfg.Ignore) == 0 && len(cfg.Only) == 0 && len(cfg.Order) == 0 {
		return nil
	}
	return &openrouter.ProviderPreferences{
		AllowFallbacks: cfg.AllowFallbacks,
		Ignore:         append([]string(nil), cfg.Ignore...),
		Only:           append([]string(nil), cfg.Only...),
		Order:          append([]string(nil), cfg.Order...),
	}
}

func judgeSystemPrompt() string {
	return "You are evaluating benchmark outputs from different models. Return strict minified JSON only, with no markdown fences and no commentary. Rank candidates by overall quality for the task. Score each candidate from 1 to 10 on correctness, completeness, usefulness, and style."
}

func buildJudgePrompt(task TaskConfig, candidates []*AttemptResult) string {
	var b strings.Builder
	b.WriteString("Task:\n")
	b.WriteString(task.Title)
	if strings.TrimSpace(task.Category) != "" {
		b.WriteString(" [")
		b.WriteString(task.Category)
		b.WriteString("]")
	}
	b.WriteString("\n\nConversation prompts:\n")
	for i, turn := range task.Turns {
		fmt.Fprintf(&b, "%d. %s\n", i+1, turn.Prompt)
	}
	if strings.TrimSpace(task.JudgeFocus) != "" {
		b.WriteString("\nJudge focus:\n")
		b.WriteString(task.JudgeFocus)
		b.WriteString("\n")
	}
	b.WriteString("\nCandidates:\n")
	for _, candidate := range candidates {
		fmt.Fprintf(&b, "\n[%s]\n", candidate.ModelID)
		if len(candidate.Transcript) > 1 {
			for i, turn := range candidate.Transcript {
				fmt.Fprintf(&b, "Turn %d output:\n%s\n", i+1, strings.TrimSpace(turn.Output))
			}
		} else {
			b.WriteString(strings.TrimSpace(candidate.FinalOutput))
			b.WriteString("\n")
		}
	}
	b.WriteString("\nReturn JSON with shape {\"winner\":\"...\",\"ranking\":[...],\"scores\":{\"candidate-id\":{\"correctness\":0,\"completeness\":0,\"usefulness\":0,\"style\":0}},\"summary\":\"...\"}. Use the candidate ids exactly as shown.\n")
	return b.String()
}

func extractJudgeText(resp openrouter.ResponsesResponse) string {
	var b strings.Builder
	for _, it := range resp.Output {
		if it.Type != "message" {
			continue
		}
		for _, part := range it.Content {
			if part.Type == "output_text" || part.Type == "input_text" {
				b.WriteString(part.Text)
			}
		}
	}
	return strings.TrimSpace(b.String())
}

func extractJudgeJSON(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return s
	}
	if fenced := extractFencedBlock(s); fenced != "" {
		s = fenced
	}
	if obj := extractBalancedJSONObject(s); obj != "" {
		return obj
	}
	return s
}

func extractFencedBlock(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) < 3 {
		return ""
	}
	if !strings.HasPrefix(strings.TrimSpace(lines[0]), "```") {
		return ""
	}
	end := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "```" {
			end = i
			break
		}
	}
	if end <= 1 {
		return ""
	}
	return strings.TrimSpace(strings.Join(lines[1:end], "\n"))
}

func extractBalancedJSONObject(s string) string {
	start := strings.IndexByte(s, '{')
	if start < 0 {
		return ""
	}
	depth := 0
	inString := false
	escaped := false
	for i := start; i < len(s); i++ {
		ch := s[i]
		if inString {
			if escaped {
				escaped = false
				continue
			}
			if ch == '\\' {
				escaped = true
				continue
			}
			if ch == '"' {
				inString = false
			}
			continue
		}
		switch ch {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[start : i+1]
			}
		}
	}
	return strings.TrimSpace(s[start:])
}

func judgeGroups(pool []*AttemptResult, size int) [][]*AttemptResult {
	if len(pool) <= size {
		return [][]*AttemptResult{pool}
	}
	out := make([][]*AttemptResult, 0, (len(pool)+size-1)/size)
	for i := 0; i < len(pool); i += size {
		end := i + size
		if end > len(pool) {
			end = len(pool)
		}
		out = append(out, pool[i:end])
	}
	return out
}

func rankedIDs(points map[string]float64) []string {
	ids := make([]string, 0, len(points))
	for id := range points {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		if points[ids[i]] == points[ids[j]] {
			return ids[i] < ids[j]
		}
		return points[ids[i]] > points[ids[j]]
	})
	return ids
}

func judgeIDs(judges []JudgeConfig) []string {
	out := make([]string, 0, len(judges))
	for _, j := range judges {
		out = append(out, j.ID)
	}
	return out
}
