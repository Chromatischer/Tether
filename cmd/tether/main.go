package main

import (
	"context"
	"errors"
	"flag"
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
	var cfgPath string
	var gradientTest bool
	flag.StringVar(&cfgPath, "config", "./config/tether.yaml", "path to tether config")
	flag.BoolVar(&gradientTest, "gradient-test", false, "run the SSH portal in gradient test mode")
	flag.Parse()

	cfg, err := config.Load(cfgPath)
	if err != nil {
		log.Fatal("failed to load config", "error", err)
	}

	logger := log.NewWithOptions(os.Stderr, log.Options{Level: cfg.LogLevelParsed})
	log.SetDefault(logger)

	database, err := db.Open(cfg.DB.Path)
	if err != nil {
		log.Fatal("failed to open db", "error", err)
	}
	defer database.Close()

	if err := db.Migrate(database); err != nil {
		log.Fatal("failed to run migrations", "error", err)
	}

	ag := agent.New(cfg, database)

	sigGW := signalgw.NewGateway(cfg, database, ag)
	discGW := discordgw.NewGateway(cfg, database, ag)

	srv, err := portal.NewServer(cfg, database, ag, discGW, gradientTest)
	if err != nil {
		log.Fatal("failed to create ssh portal server", "error", err)
	}
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
}
