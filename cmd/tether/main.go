package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"charm.land/log/v2"

	"tether/internal/agent"
	"tether/internal/config"
	"tether/internal/db"
	discordgw "tether/internal/discord"
	"tether/internal/portal"
	"tether/internal/proactive"
	signalgw "tether/internal/signal"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "benchmark" {
		if err := runBenchmarkCommand(os.Args[2:]); err != nil {
			log.Fatal("benchmark command failed", "error", err)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "serve" {
		if err := runServeCommand(os.Args[2:]); err != nil {
			log.Fatal("failed to run server", "error", err)
		}
		return
	}
	if err := runServeCommand(os.Args[1:]); err != nil {
		log.Fatal("failed to run server", "error", err)
	}
}

func runServeCommand(args []string) error {
	var cfgPath string
	var gradientTest bool
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.StringVar(&cfgPath, "config", "./config/tether.yaml", "path to tether config")
	fs.BoolVar(&gradientTest, "gradient-test", false, "run the SSH portal in gradient test mode")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}

	logger := log.NewWithOptions(os.Stderr, log.Options{Level: cfg.LogLevelParsed})
	log.SetDefault(logger)

	database, err := db.Open(cfg.DB.Path)
	if err != nil {
		return err
	}
	defer database.Close()

	if err := db.Migrate(database); err != nil {
		return err
	}

	ag := agent.New(cfg, database)
	srv, err := portal.NewServer(cfg, database, ag, gradientTest)
	if err != nil {
		return err
	}

	sigGW := signalgw.NewGateway(cfg, database, ag)
	discGW := discordgw.NewGateway(cfg, database, ag)
	pro := proactive.NewScheduler(database, ag, ag, ag.Subagents(), cfg.Paths.DataDir, 1*time.Minute)

	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigGW.Start(ctx)
	discGW.Start(ctx)
	go pro.Start(ctx)

	go func() {
		log.Info("starting ssh portal", "addr", cfg.SSH.ListenAddr)
		if err := srv.ListenAndServe(); err != nil {
			if errors.Is(err, portal.ErrServerClosed) {
				return
			}
			log.Error("ssh portal server error", "error", err)
			done <- nil
		}
	}()

	<-done
	log.Info("shutting down")
	cancel()
	ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel2()
	if err := srv.Shutdown(ctx2); err != nil {
		log.Error("graceful ssh portal shutdown failed; forcing close", "error", err)
		if cerr := srv.Close(); cerr != nil && !errors.Is(cerr, portal.ErrServerClosed) {
			log.Error("failed to close ssh portal", "error", cerr)
		}
	}
	return nil
}

func benchmarkUsage() string {
	return "usage: tether benchmark <run|tui> [-config path] [-bench path]"
}

func errUsage(msg string) error {
	if msg == "" {
		return fmt.Errorf("%s", benchmarkUsage())
	}
	return fmt.Errorf("%s\n%s", msg, benchmarkUsage())
}
