package benchmark

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"tether/internal/store"
	"tether/internal/testutil"
)

func TestLoadConfigAppliesDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bench.toml")
	if err := os.WriteFile(path, []byte(`
[[models]]
id = "a"
model = "openai/gpt-4.1-mini"

[[tasks]]
id = "task"

[[tasks.turns]]
prompt = "hi"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.MaxParallel != defaultMaxParallel {
		t.Fatalf("MaxParallel=%d want %d", cfg.MaxParallel, defaultMaxParallel)
	}
	if cfg.JudgeGroupSize != defaultJudgeGroup {
		t.Fatalf("JudgeGroupSize=%d want %d", cfg.JudgeGroupSize, defaultJudgeGroup)
	}
	d, err := cfg.TaskTimeoutFor(cfg.Tasks[0])
	if err != nil {
		t.Fatalf("TaskTimeoutFor: %v", err)
	}
	if d != 3*time.Minute {
		t.Fatalf("timeout=%v want %v", d, 3*time.Minute)
	}
}

func TestLoadConfigParsesPrefillHistoryTokens(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bench.toml")
	if err := os.WriteFile(path, []byte(`
[[models]]
id = "a"
model = "openai/gpt-4.1-mini"

[[tasks]]
id = "long"
prefill_history_tokens = 100000

[[tasks.turns]]
prompt = "summarize the latest request"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if got := cfg.Tasks[0].PrefillHistoryTokens; got != 100000 {
		t.Fatalf("PrefillHistoryTokens=%d want 100000", got)
	}
}

func TestPrefillConversationHistoryAddsRandomHistoryToConversation(t *testing.T) {
	d := testutil.OpenTestDB(t)
	u, err := store.CreateUser(d, "bench-prefill", "pw")
	if err != nil {
		t.Fatal(err)
	}
	conv, err := store.CreateConversation(d, u.ID, "prefill")
	if err != nil {
		t.Fatal(err)
	}
	r := &Runner{db: d}
	messages, tokens, err := r.prefillConversationHistory("run-1", conv.ID, TaskConfig{
		ID:                   "long-context",
		PrefillHistoryTokens: 1200,
	})
	if err != nil {
		t.Fatalf("prefillConversationHistory: %v", err)
	}
	if messages == 0 {
		t.Fatal("expected prefill messages")
	}
	if tokens < 1200 {
		t.Fatalf("tokens=%d want at least 1200", tokens)
	}
	history, err := store.ListMessagesAfterID(d, conv.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != messages {
		t.Fatalf("history len=%d want %d", len(history), messages)
	}
	if history[0].Role != "user" {
		t.Fatalf("first role=%q want user", history[0].Role)
	}
	if messages > 1 && history[1].Role != "assistant" {
		t.Fatalf("second role=%q want assistant", history[1].Role)
	}
}

func TestAttemptParallelismLimitDefaultsToModelCount(t *testing.T) {
	r := &Runner{bench: &Config{
		MaxParallel: 0,
		Models: []ModelConfig{
			{ID: "a", Model: "model/a"},
			{ID: "b", Model: "model/b"},
			{ID: "c", Model: "model/c"},
		},
	}}
	if got := r.attemptParallelismLimit(); got != 3 {
		t.Fatalf("attemptParallelismLimit=%d want 3", got)
	}

	r.bench.MaxParallel = 2
	if got := r.attemptParallelismLimit(); got != 2 {
		t.Fatalf("attemptParallelismLimit=%d want explicit limit 2", got)
	}
}

func TestLoadConfigResolvesPromptFileRelative(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bench.toml")
	if err := os.WriteFile(path, []byte(`
[[models]]
id = "a"
model = "openai/gpt-4.1-mini"
prompt_file = "prompts/custom.md"

[[tasks]]
id = "task"

[[tasks.turns]]
prompt = "hi"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	want := filepath.Join(dir, "prompts", "custom.md")
	if cfg.Models[0].PromptFile != want {
		t.Fatalf("PromptFile=%q want %q", cfg.Models[0].PromptFile, want)
	}
}

func TestRepositoryBenchmarkConfigsLoad(t *testing.T) {
	for _, path := range []string{
		filepath.Join("..", "..", "config", "benchmark.toml"),
		filepath.Join("..", "..", "config", "benchmark.example.toml"),
	} {
		cfg, err := LoadConfig(path)
		if err != nil {
			t.Fatalf("LoadConfig(%s): %v", path, err)
		}
		if len(cfg.Tasks) < 10 {
			t.Fatalf("LoadConfig(%s) tasks=%d want at least 10", path, len(cfg.Tasks))
		}
	}
}
