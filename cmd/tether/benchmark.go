package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"charm.land/log/v2"

	"tether/internal/benchmark"
	"tether/internal/config"
	"tether/internal/db"
)

func runBenchmarkCommand(args []string) error {
	if len(args) == 0 {
		return errUsage("")
	}
	switch args[0] {
	case "run":
		return runBenchmarkCLI(args[1:])
	case "tui":
		return runBenchmarkTUICommand(args[1:])
	default:
		return errUsage("unknown benchmark subcommand")
	}
}

func runBenchmarkCLI(args []string) error {
	cfg, benchCfg, benchPath, database, err := loadBenchmarkDeps(args)
	if err != nil {
		return err
	}
	defer database.Close()

	logger := log.NewWithOptions(os.Stderr, log.Options{Level: cfg.LogLevelParsed})
	log.SetDefault(logger)

	runner := benchmark.NewRunner(cfg, database, benchCfg, benchPath)
	ctx := context.Background()
	result, err := runner.Run(ctx, func(ev benchmark.ProgressEvent) {
		line := ev.Stage
		if ev.TaskID != "" || ev.ModelID != "" || ev.JudgeID != "" {
			line += " "
			if ev.TaskID != "" {
				line += "task=" + ev.TaskID + " "
			}
			if ev.ModelID != "" {
				line += "model=" + ev.ModelID + " "
			}
			if ev.JudgeID != "" {
				line += "judge=" + ev.JudgeID + " "
			}
		}
		if ev.Message != "" {
			line += strings.TrimSpace(ev.Message)
		}
		if ev.Err != "" {
			line += " err=" + ev.Err
		}
		fmt.Fprintln(os.Stderr, strings.TrimSpace(line))
	})
	if err != nil {
		return err
	}
	printBenchmarkSummary(result)
	return nil
}

func loadBenchmarkDeps(args []string) (*config.Config, *benchmark.Config, string, *sql.DB, error) {
	fs := flag.NewFlagSet("benchmark", flag.ContinueOnError)
	var cfgPath string
	var benchPath string
	fs.StringVar(&cfgPath, "config", "./config/tether.yaml", "path to tether config")
	fs.StringVar(&benchPath, "bench", "./config/benchmark.toml", "path to benchmark TOML")
	if err := fs.Parse(args); err != nil {
		return nil, nil, "", nil, err
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return nil, nil, "", nil, err
	}
	benchCfg, err := benchmark.LoadConfig(benchPath)
	if err != nil {
		return nil, nil, "", nil, err
	}
	database, err := db.Open(cfg.DB.Path)
	if err != nil {
		return nil, nil, "", nil, err
	}
	if err := db.Migrate(database); err != nil {
		_ = database.Close()
		return nil, nil, "", nil, err
	}
	return cfg, benchCfg, benchPath, database, nil
}

func printBenchmarkSummary(result *benchmark.RunResult) {
	fmt.Printf("Run %s completed in %s\n", result.RunID, result.Duration.Round(time.Second))
	fmt.Printf("Results: %s\n\n", result.ResultsPath)
	for i, summary := range result.ModelSummaries {
		fmt.Printf("%d. %s (%s) overall=%.3f objective=%.3f subjective=%.3f completed=%d/%d tokens=%d cost=%.4f duration=%s\n",
			i+1,
			summary.ModelLabel,
			summary.ModelName,
			summary.AverageOverall,
			summary.AverageObjective,
			summary.AverageSubjective,
			summary.TasksCompleted,
			summary.TasksTotal,
			summary.TotalTokens,
			summary.TotalCost,
			summary.TotalDuration.Round(time.Second),
		)
	}
}
