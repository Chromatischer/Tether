package benchmark

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
)

const (
	defaultResultsDir    = "benchmarks"
	defaultMaxParallel   = 0
	defaultJudgeParallel = 2
	defaultJudgeGroup    = 5
)

type Config struct {
	Name           string        `toml:"name"`
	Description    string        `toml:"description"`
	ResultsDir     string        `toml:"results_dir"`
	MaxParallel    int           `toml:"max_parallel"`
	JudgeParallel  int           `toml:"judge_parallel"`
	JudgeGroupSize int           `toml:"judge_group_size"`
	TaskTimeout    string        `toml:"task_timeout"`
	Scoring        ScoringConfig `toml:"scoring"`
	Models         []ModelConfig `toml:"models"`
	Judges         []JudgeConfig `toml:"judges"`
	Tasks          []TaskConfig  `toml:"tasks"`
}

type ScoringConfig struct {
	ObjectiveWeight   float64       `toml:"objective_weight"`
	SubjectiveWeight  float64       `toml:"subjective_weight"`
	CompletionWeight  float64       `toml:"completion_weight"`
	DurationWeight    float64       `toml:"duration_weight"`
	LatencyWeight     float64       `toml:"latency_weight"`
	TokenWeight       float64       `toml:"token_weight"`
	CostWeight        float64       `toml:"cost_weight"`
	EfficiencyWeight  float64       `toml:"efficiency_weight"`
	JudgeRankWeight   float64       `toml:"judge_rank_weight"`
	JudgeRubricWeight float64       `toml:"judge_rubric_weight"`
	Rubric            RubricWeights `toml:"rubric"`
}

type RubricWeights struct {
	Correctness  float64 `toml:"correctness"`
	Completeness float64 `toml:"completeness"`
	Usefulness   float64 `toml:"usefulness"`
	Style        float64 `toml:"style"`
}

type ModelConfig struct {
	ID              string            `toml:"id"`
	Label           string            `toml:"label"`
	Model           string            `toml:"model"`
	Prompt          string            `toml:"prompt"`
	PromptFile      string            `toml:"prompt_file"`
	Provider        ProviderConfig    `toml:"provider"`
	MaxOutputTokens int               `toml:"max_output_tokens"`
	Metadata        map[string]string `toml:"metadata"`
}

type JudgeConfig struct {
	ID       string         `toml:"id"`
	Label    string         `toml:"label"`
	Model    string         `toml:"model"`
	Weight   float64        `toml:"weight"`
	Provider ProviderConfig `toml:"provider"`
}

type ProviderConfig struct {
	AllowFallbacks *bool    `toml:"allow_fallbacks"`
	Ignore         []string `toml:"ignore"`
	Only           []string `toml:"only"`
	Order          []string `toml:"order"`
}

type TaskConfig struct {
	ID                   string     `toml:"id"`
	Title                string     `toml:"title"`
	Category             string     `toml:"category"`
	Description          string     `toml:"description"`
	JudgeFocus           string     `toml:"judge_focus"`
	Timeout              string     `toml:"timeout"`
	PrefillHistoryTokens int        `toml:"prefill_history_tokens"`
	Tags                 []string   `toml:"tags"`
	Turns                []TaskTurn `toml:"turns"`
}

type TaskTurn struct {
	Prompt string `toml:"prompt"`
}

func LoadConfig(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := toml.Unmarshal(b, &cfg); err != nil {
		return nil, err
	}
	if err := cfg.applyDefaults(filepath.Dir(path)); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (c *Config) applyDefaults(baseDir string) error {
	if c == nil {
		return fmt.Errorf("benchmark config is nil")
	}
	if strings.TrimSpace(c.Name) == "" {
		c.Name = "tether-benchmark"
	}
	if strings.TrimSpace(c.ResultsDir) == "" {
		c.ResultsDir = defaultResultsDir
	}
	if c.MaxParallel < 0 {
		c.MaxParallel = defaultMaxParallel
	}
	if c.JudgeParallel <= 0 {
		c.JudgeParallel = defaultJudgeParallel
	}
	if c.JudgeGroupSize <= 0 {
		c.JudgeGroupSize = defaultJudgeGroup
	}
	if c.JudgeGroupSize < 2 {
		return fmt.Errorf("judge_group_size must be at least 2")
	}

	applyScoringDefaults(&c.Scoring)
	for i := range c.Models {
		m := &c.Models[i]
		if strings.TrimSpace(m.Label) == "" {
			m.Label = strings.TrimSpace(m.ID)
		}
		if m.PromptFile != "" && !filepath.IsAbs(m.PromptFile) {
			m.PromptFile = filepath.Clean(filepath.Join(baseDir, m.PromptFile))
		}
	}
	for i := range c.Judges {
		j := &c.Judges[i]
		if strings.TrimSpace(j.Label) == "" {
			j.Label = strings.TrimSpace(j.ID)
		}
		if j.Weight <= 0 {
			j.Weight = 1
		}
	}
	for i := range c.Tasks {
		t := &c.Tasks[i]
		if strings.TrimSpace(t.Title) == "" {
			t.Title = strings.TrimSpace(t.ID)
		}
	}
	return c.Validate()
}

func (c *Config) Validate() error {
	if len(c.Models) == 0 {
		return fmt.Errorf("benchmark config requires at least one model")
	}
	if len(c.Tasks) == 0 {
		return fmt.Errorf("benchmark config requires at least one task")
	}
	seen := map[string]bool{}
	for _, m := range c.Models {
		if strings.TrimSpace(m.ID) == "" {
			return fmt.Errorf("model id is required")
		}
		if strings.TrimSpace(m.Model) == "" {
			return fmt.Errorf("model %q is missing model", m.ID)
		}
		key := "model:" + strings.ToLower(strings.TrimSpace(m.ID))
		if seen[key] {
			return fmt.Errorf("duplicate model id %q", m.ID)
		}
		seen[key] = true
	}
	for _, j := range c.Judges {
		if strings.TrimSpace(j.ID) == "" {
			return fmt.Errorf("judge id is required")
		}
		if strings.TrimSpace(j.Model) == "" {
			return fmt.Errorf("judge %q is missing model", j.ID)
		}
		key := "judge:" + strings.ToLower(strings.TrimSpace(j.ID))
		if seen[key] {
			return fmt.Errorf("duplicate judge id %q", j.ID)
		}
		seen[key] = true
	}
	for _, t := range c.Tasks {
		if strings.TrimSpace(t.ID) == "" {
			return fmt.Errorf("task id is required")
		}
		if len(t.Turns) == 0 {
			return fmt.Errorf("task %q requires at least one turn", t.ID)
		}
		for _, turn := range t.Turns {
			if strings.TrimSpace(turn.Prompt) == "" {
				return fmt.Errorf("task %q contains an empty turn prompt", t.ID)
			}
		}
		if t.PrefillHistoryTokens < 0 {
			return fmt.Errorf("task %q prefill_history_tokens must be non-negative", t.ID)
		}
		if _, err := c.TaskTimeoutFor(t); err != nil {
			return fmt.Errorf("task %q timeout: %w", t.ID, err)
		}
	}
	return nil
}

func (c *Config) TaskTimeoutFor(task TaskConfig) (time.Duration, error) {
	raw := strings.TrimSpace(task.Timeout)
	if raw == "" {
		raw = strings.TrimSpace(c.TaskTimeout)
	}
	if raw == "" {
		return 3 * time.Minute, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, err
	}
	return d, nil
}

func applyScoringDefaults(cfg *ScoringConfig) {
	if cfg.ObjectiveWeight == 0 {
		cfg.ObjectiveWeight = 0.35
	}
	if cfg.SubjectiveWeight == 0 {
		cfg.SubjectiveWeight = 0.65
	}
	if cfg.CompletionWeight == 0 {
		cfg.CompletionWeight = 0.2
	}
	if cfg.DurationWeight == 0 {
		cfg.DurationWeight = 0.25
	}
	if cfg.LatencyWeight == 0 {
		cfg.LatencyWeight = 0.1
	}
	if cfg.TokenWeight == 0 {
		cfg.TokenWeight = 0.2
	}
	if cfg.CostWeight == 0 {
		cfg.CostWeight = 0.15
	}
	if cfg.EfficiencyWeight == 0 {
		cfg.EfficiencyWeight = 0.1
	}
	if cfg.JudgeRankWeight == 0 {
		cfg.JudgeRankWeight = 0.45
	}
	if cfg.JudgeRubricWeight == 0 {
		cfg.JudgeRubricWeight = 0.55
	}
	if cfg.Rubric.Correctness == 0 {
		cfg.Rubric.Correctness = 0.4
	}
	if cfg.Rubric.Completeness == 0 {
		cfg.Rubric.Completeness = 0.2
	}
	if cfg.Rubric.Usefulness == 0 {
		cfg.Rubric.Usefulness = 0.25
	}
	if cfg.Rubric.Style == 0 {
		cfg.Rubric.Style = 0.15
	}
}
