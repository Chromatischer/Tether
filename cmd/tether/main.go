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
	"tether/internal/term"
)

func main() {
	var cfgPath string
	var gradientTest bool
	var termMode bool
	var continueConv bool
	var textMode bool
	flag.StringVar(&cfgPath, "config", "./config/tether.yaml", "path to tether config")
	flag.BoolVar(&gradientTest, "gradient-test", false, "run the SSH portal in gradient test mode")
	flag.BoolVar(&termMode, "term", false, "run in terminal (headless) mode")
	flag.BoolVar(&continueConv, "continue", false, "attach to existing conversation (--term mode)")
	flag.BoolVar(&textMode, "text", false, "disable ANSI styling (--term mode)")
	flag.Parse()

	cfg, err := config.Load(cfgPath)
	if err != nil {
		log.Fatal("failed to load config", "error", err)
	}

	log.SetDefault(log.NewWithOptions(os.Stderr, log.Options{Level: cfg.LogLevelParsed}))

	database, err := db.Open(cfg.DB.Path)
	if err != nil {
		log.Fatal("failed to open db", "error", err)
	}
	defer database.Close()

	if err := db.Migrate(database); err != nil {
		log.Fatal("failed to run migrations", "error", err)
	}

	ag := agent.New(cfg, database)

	if termMode {
		if err := term.Run(context.Background(), cfg, database, ag, continueConv, textMode); err != nil {
			log.Error("terminal mode error", "error", err)
		}
		return
	}

	srv, err := portal.NewServer(cfg, database, ag, gradientTest)
	if err != nil {
		log.Fatal("failed to create ssh portal server", "error", err)
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
}
