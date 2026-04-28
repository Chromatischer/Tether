package main

import (
	tea "charm.land/bubbletea/v2"

	"tether/internal/benchmark"
	"tether/internal/benchmarkui"
)

func runBenchmarkTUICommand(args []string) error {
	cfg, benchCfg, benchPath, database, err := loadBenchmarkDeps(args)
	if err != nil {
		return err
	}
	defer database.Close()

	runner := benchmark.NewRunner(cfg, database, benchCfg, benchPath)
	prog := tea.NewProgram(benchmarkui.New(runner))
	_, err = prog.Run()
	return err
}
