package benchmark

import (
	"context"
	"crypto/sha1"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"tether/internal/agent"
	"tether/internal/config"
	"tether/internal/llm/openrouter"
	"tether/internal/store"
	"tether/internal/systemprompt"
	"tether/internal/userspace"
)

type Runner struct {
	cfg        *config.Config
	db         *sql.DB
	bench      *Config
	configPath string
}

type Plan struct {
	Models []ModelConfig
	Tasks  []TaskConfig
}

func NewRunner(cfg *config.Config, db *sql.DB, bench *Config, configPath string) *Runner {
	return &Runner{cfg: cfg, db: db, bench: bench, configPath: configPath}
}

func (r *Runner) Plan() Plan {
	if r == nil || r.bench == nil {
		return Plan{}
	}
	return Plan{
		Models: append([]ModelConfig(nil), r.bench.Models...),
		Tasks:  append([]TaskConfig(nil), r.bench.Tasks...),
	}
}

func (r *Runner) Run(ctx context.Context, emit func(ProgressEvent)) (*RunResult, error) {
	if r == nil || r.cfg == nil || r.db == nil || r.bench == nil {
		return nil, fmt.Errorf("benchmark runner is not initialized")
	}
	run := &RunResult{
		RunID:       newRunID(),
		Name:        r.bench.Name,
		Description: r.bench.Description,
		ConfigPath:  r.configPath,
		StartedAt:   time.Now().UTC(),
	}
	resultsDir := r.resultsDir()
	if err := os.MkdirAll(resultsDir, 0o755); err != nil {
		return nil, err
	}
	run.ResultsPath = filepath.Join(resultsDir, run.RunID+".json")
	progress(emit, ProgressEvent{Stage: "run_started", RunID: run.RunID, Message: "benchmark run started"})
	if err := r.preflightAuth(ctx); err != nil {
		progress(emit, ProgressEvent{Stage: "run_failed", RunID: run.RunID, Err: err.Error(), Message: "benchmark preflight failed"})
		return nil, err
	}

	attempts := make([]AttemptResult, 0, len(r.bench.Models)*len(r.bench.Tasks))
	for _, taskCfg := range r.bench.Tasks {
		taskAttempts, err := r.runTaskAttempts(ctx, run.RunID, taskCfg, emit)
		attempts = append(attempts, taskAttempts...)
		if err != nil {
			return nil, err
		}
	}
	sort.Slice(attempts, func(i, j int) bool {
		if attempts[i].TaskID == attempts[j].TaskID {
			return attempts[i].ModelID < attempts[j].ModelID
		}
		return attempts[i].TaskID < attempts[j].TaskID
	})
	run.Attempts = attempts

	if len(r.bench.Judges) > 0 {
		if err := r.applyJudging(ctx, run, emit); err != nil {
			progress(emit, ProgressEvent{Stage: "judge_failed", RunID: run.RunID, Err: err.Error(), Message: "judging failed"})
			return nil, err
		}
	}
	r.applyScores(run)
	run.TaskSummaries = r.buildTaskSummaries(run.Attempts)
	run.ModelSummaries = r.buildModelSummaries(run.Attempts)
	run.CompletedAt = time.Now().UTC()
	run.Duration = run.CompletedAt.Sub(run.StartedAt)
	if err := r.save(run); err != nil {
		return nil, err
	}
	progress(emit, ProgressEvent{Stage: "run_finished", RunID: run.RunID, Message: run.ResultsPath})
	return run, nil
}

func (r *Runner) runTaskAttempts(ctx context.Context, runID string, taskCfg TaskConfig, emit func(ProgressEvent)) ([]AttemptResult, error) {
	attempts := make([]AttemptResult, 0, len(r.bench.Models))
	var attemptsMu sync.Mutex

	eg, egCtx := errgroup.WithContext(ctx)
	eg.SetLimit(r.attemptParallelismLimit())
	fatalErrs := make(chan error, 1)
	for _, modelCfg := range r.bench.Models {
		modelCfg := modelCfg
		eg.Go(func() error {
			progress(emit, ProgressEvent{Stage: "attempt_started", RunID: runID, ModelID: modelCfg.ID, TaskID: taskCfg.ID, Message: "attempt started"})
			attempt, err := r.runAttempt(egCtx, runID, modelCfg, taskCfg)
			attemptsMu.Lock()
			attempts = append(attempts, attempt)
			attemptsMu.Unlock()
			if err != nil {
				progress(emit, ProgressEvent{Stage: "attempt_failed", RunID: runID, ModelID: modelCfg.ID, TaskID: taskCfg.ID, Err: err.Error(), Message: "attempt failed"})
				if isFatalBenchmarkError(err) {
					select {
					case fatalErrs <- err:
					default:
					}
					return err
				}
				return nil
			}
			progress(emit, ProgressEvent{Stage: "attempt_finished", RunID: runID, ModelID: modelCfg.ID, TaskID: taskCfg.ID, Message: "attempt finished"})
			return nil
		})
	}
	if err := eg.Wait(); err != nil {
		select {
		case fatal := <-fatalErrs:
			return attempts, fatal
		default:
			return attempts, err
		}
	}
	return attempts, nil
}

func (r *Runner) attemptParallelismLimit() int {
	if r == nil || r.bench == nil {
		return 1
	}
	if r.bench.MaxParallel > 0 {
		return r.bench.MaxParallel
	}
	if n := len(r.bench.Models); n > 0 {
		return n
	}
	return 1
}

func (r *Runner) preflightAuth(ctx context.Context) error {
	if r == nil || r.cfg == nil {
		return fmt.Errorf("benchmark runner is not initialized")
	}
	model := strings.TrimSpace(r.cfg.OpenRouter.Model)
	if model == "" && len(r.bench.Models) > 0 {
		model = strings.TrimSpace(r.bench.Models[0].Model)
	}
	if model == "" {
		return fmt.Errorf("no OpenRouter model configured for benchmark preflight")
	}
	client := openrouter.New(r.cfg.OpenRouter.BaseURL, r.cfg.OpenRouter.APIKey, "Tether Benchmark")
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	_, err := client.Responses(ctx, openrouter.ResponsesRequest{
		Model:      model,
		Input:      []openrouter.ResponseItem{{Type: "message", Role: "user", Content: []openrouter.ContentPart{{Type: "input_text", Text: "ping"}}}},
		ToolChoice: "none",
	})
	if err == nil {
		return nil
	}
	var httpErr *openrouter.HTTPError
	if errors.As(err, &httpErr) && httpErr.StatusCode == 401 {
		return fmt.Errorf("OpenRouter authentication failed: %s", strings.TrimSpace(httpErr.Message()))
	}
	return err
}

func isFatalBenchmarkError(err error) bool {
	var httpErr *openrouter.HTTPError
	return errors.As(err, &httpErr) && httpErr.StatusCode == 401
}

func (r *Runner) runAttempt(ctx context.Context, runID string, modelCfg ModelConfig, taskCfg TaskConfig) (AttemptResult, error) {
	attempt := AttemptResult{
		ModelID:    modelCfg.ID,
		ModelLabel: modelCfg.Label,
		ModelName:  modelCfg.Model,
		TaskID:     taskCfg.ID,
		TaskTitle:  taskCfg.Title,
		Category:   taskCfg.Category,
		StartedAt:  time.Now().UTC(),
	}
	timeout, err := r.bench.TaskTimeoutFor(taskCfg)
	if err != nil {
		attempt.Error = err.Error()
		return attempt, err
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	runCfg := cloneConfig(r.cfg, modelCfg)
	ag := agent.New(runCfg, r.db)
	user, err := store.CreateUser(r.db, benchmarkUsername(runID, modelCfg.ID, taskCfg.ID), "benchmark")
	if err != nil {
		attempt.Error = err.Error()
		return attempt, err
	}
	dirs := userspace.ForUser(runCfg.Paths.DataDir, user.ID)
	if err := userspace.Ensure(dirs); err != nil {
		attempt.Error = err.Error()
		return attempt, err
	}
	if err := applyPromptVariant(dirs, modelCfg); err != nil {
		attempt.Error = err.Error()
		return attempt, err
	}
	conv, err := store.CreateConversation(r.db, user.ID, "benchmark "+taskCfg.Title)
	if err != nil {
		attempt.Error = err.Error()
		return attempt, err
	}
	ag.ResetConversationSession(conv.ID)

	prefillMessages, prefillTokens, err := r.prefillConversationHistory(runID, conv.ID, taskCfg)
	if err != nil {
		attempt.Error = err.Error()
		return attempt, err
	}
	attempt.PrefillMessages = prefillMessages
	attempt.PrefillTokens = prefillTokens

	var totalFirstToken time.Duration
	transcript := make([]TranscriptTurn, 0, len(taskCfg.Turns))
	toolNames := make([]string, 0, 8)
	for _, turn := range taskCfg.Turns {
		if err := store.AddMessage(r.db, conv.ID, "user", turn.Prompt); err != nil {
			attempt.Error = err.Error()
			return finishAttempt(attempt, transcript, toolNames), err
		}
		replyStart := time.Now()
		var firstTokenAt time.Time
		reply, err := ag.ReplyStream(ctx, agent.ReplyParams{
			UserID:         user.ID,
			ConversationID: conv.ID,
			Text:           turn.Prompt,
		}, func(ev agent.StreamEvent) {
			if firstTokenAt.IsZero() && (ev.Type == "assistant_delta" || ev.Type == "done") {
				firstTokenAt = time.Now()
			}
		})
		if !firstTokenAt.IsZero() {
			totalFirstToken += firstTokenAt.Sub(replyStart)
		}
		if err != nil {
			attempt.Error = err.Error()
			attempt.Completed = false
			return finishAttempt(attempt, transcript, toolNames), err
		}
		if err := store.AddMessage(r.db, conv.ID, "assistant", reply.Text); err != nil {
			attempt.Error = err.Error()
			return finishAttempt(attempt, transcript, toolNames), err
		}
		for _, tc := range reply.ToolCalls {
			if name := strings.TrimSpace(tc.Name); name != "" {
				toolNames = append(toolNames, name)
			}
		}
		transcript = append(transcript, TranscriptTurn{Prompt: turn.Prompt, Output: reply.Text})
		attempt.FinalOutput = reply.Text
	}

	status := ag.SessionStatus(user.ID, conv.ID)
	attempt.Transcript = transcript
	attempt.ToolCalls = toolNames
	attempt.TotalToolCalls = status.TotalToolCalls
	attempt.InputTokens = status.TotalInputTokens
	attempt.OutputTokens = status.TotalOutputTokens
	attempt.TotalTokens = status.TotalTokens
	attempt.TotalCost = status.TotalCost
	attempt.ContextPct = status.LastContextPct
	if len(taskCfg.Turns) > 0 {
		attempt.FirstTokenLatency = totalFirstToken / time.Duration(len(taskCfg.Turns))
	}
	attempt.Completed = strings.TrimSpace(attempt.FinalOutput) != ""
	return finishAttempt(attempt, transcript, toolNames), nil
}

func finishAttempt(attempt AttemptResult, transcript []TranscriptTurn, toolNames []string) AttemptResult {
	attempt.Transcript = transcript
	attempt.ToolCalls = toolNames
	attempt.CompletedAt = time.Now().UTC()
	attempt.Duration = attempt.CompletedAt.Sub(attempt.StartedAt)
	return attempt
}

func (r *Runner) prefillConversationHistory(runID string, conversationID int64, task TaskConfig) (int, int, error) {
	target := task.PrefillHistoryTokens
	if target <= 0 {
		return 0, 0, nil
	}
	rng := rand.New(rand.NewSource(prefillSeed(runID, task.ID)))
	total := 0
	messages := 0
	for total < target {
		role := "user"
		if messages%2 == 1 {
			role = "assistant"
		}
		content := randomHistoryMessage(rng, messages, role)
		if err := store.AddMessage(r.db, conversationID, role, content); err != nil {
			return messages, total, err
		}
		total += estimateBenchmarkTokens(role) + estimateBenchmarkTokens(content)
		messages++
	}
	return messages, total, nil
}

func prefillSeed(runID, taskID string) int64 {
	sum := sha1.Sum([]byte(runID + ":" + taskID + ":prefill"))
	var seed int64
	for _, b := range sum[:8] {
		seed = seed<<8 + int64(b)
	}
	return seed
}

func randomHistoryMessage(rng *rand.Rand, index int, role string) string {
	words := []string{
		"archive", "vector", "coffee", "latency", "budget", "garden", "release", "quiet",
		"signal", "branch", "calendar", "notebook", "migration", "focus", "invoice", "window",
		"ember", "socket", "checklist", "receipt", "backup", "meeting", "syntax", "draft",
		"metric", "tablet", "folder", "policy", "needle", "buffer", "kernel", "diagram",
		"summary", "router", "threshold", "context", "handoff", "review", "sandbox", "ticket",
		"memory", "deploy", "schema", "weather", "battery", "bridge", "commit", "margin",
	}
	n := 130 + rng.Intn(90)
	var b strings.Builder
	fmt.Fprintf(&b, "Archived random chat-history filler %04d (%s). It is unrelated to the benchmark request. ", index+1, role)
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(words[rng.Intn(len(words))])
		if rng.Intn(9) == 0 {
			fmt.Fprintf(&b, "-%02x", rng.Intn(256))
		}
		if rng.Intn(17) == 0 {
			b.WriteByte('.')
		}
	}
	b.WriteByte('.')
	return b.String()
}

func estimateBenchmarkTokens(s string) int {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	return (len(s)+3)/4 + 8
}

func (r *Runner) resultsDir() string {
	out := strings.TrimSpace(r.bench.ResultsDir)
	if out == "" {
		out = filepath.Join(r.cfg.Paths.DataDir, defaultResultsDir)
	}
	if !filepath.IsAbs(out) {
		out = filepath.Join(r.cfg.Paths.DataDir, out)
	}
	return filepath.Clean(out)
}

func (r *Runner) save(run *RunResult) error {
	b, err := json.MarshalIndent(run, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(run.ResultsPath, b, 0o644)
}

func cloneConfig(base *config.Config, modelCfg ModelConfig) *config.Config {
	cp := *base
	cp.OpenRouter.Model = modelCfg.Model
	cp.OpenRouter.Provider.AllowFallbacks = modelCfg.Provider.AllowFallbacks
	if modelCfg.Provider.Ignore != nil {
		cp.OpenRouter.Provider.Ignore = append([]string(nil), modelCfg.Provider.Ignore...)
	}
	if modelCfg.Provider.Only != nil {
		cp.OpenRouter.Provider.Only = append([]string(nil), modelCfg.Provider.Only...)
	}
	if modelCfg.Provider.Order != nil {
		cp.OpenRouter.Provider.Order = append([]string(nil), modelCfg.Provider.Order...)
	}
	cp.MCP.Servers = append([]config.MCPServer(nil), base.MCP.Servers...)
	return &cp
}

func applyPromptVariant(dirs userspace.Dirs, modelCfg ModelConfig) error {
	if strings.TrimSpace(modelCfg.Prompt) == "" && strings.TrimSpace(modelCfg.PromptFile) == "" {
		return userspace.EnsurePromptTemplateFile(dirs, systemprompt.TemplateChat)
	}
	if err := userspace.Ensure(dirs); err != nil {
		return err
	}
	path, ok := userspace.PromptTemplateAbsPath(dirs, systemprompt.TemplateChat)
	if !ok {
		return fmt.Errorf("chat prompt template path unavailable")
	}
	var content string
	if strings.TrimSpace(modelCfg.PromptFile) != "" {
		b, err := os.ReadFile(modelCfg.PromptFile)
		if err != nil {
			return err
		}
		content = string(b)
	} else {
		content = modelCfg.Prompt
	}
	content = strings.TrimSpace(content)
	if content == "" {
		content = systemprompt.DefaultMarkdown(systemprompt.TemplateChat)
	}
	return os.WriteFile(path, []byte(content+"\n"), 0o644)
}

func benchmarkUsername(runID, modelID, taskID string) string {
	sum := sha1.Sum([]byte(runID + ":" + modelID + ":" + taskID))
	suffix := hex.EncodeToString(sum[:])[:10]
	return fmt.Sprintf("bench_%s_%s_%s", sanitizeID(modelID), sanitizeID(taskID), suffix)
}

func sanitizeID(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := strings.Trim(b.String(), "_")
	if out == "" {
		return "x"
	}
	if len(out) > 24 {
		return out[:24]
	}
	return out
}

func newRunID() string {
	return time.Now().UTC().Format("20060102_150405")
}

func progress(emit func(ProgressEvent), ev ProgressEvent) {
	if emit != nil {
		emit(ev)
	}
}
