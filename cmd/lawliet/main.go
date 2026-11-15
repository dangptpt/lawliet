// Command lawliet runs the Lawliet in-memory vector cache server.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/tranbaohieu/lawliet/internal/command"
	"github.com/tranbaohieu/lawliet/internal/config"
	"github.com/tranbaohieu/lawliet/internal/server"
	"github.com/tranbaohieu/lawliet/internal/store"
	"github.com/tranbaohieu/lawliet/internal/version"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	cfg, err := config.Parse(args, os.Stderr)
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "lawliet:", err)
		return 2
	}
	if cfg.ShowVersion {
		fmt.Println("lawliet", version.Version)
		return 0
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	policy, err := store.ParsePolicy(cfg.MaxMemoryPol)
	if err != nil {
		fmt.Fprintln(os.Stderr, "lawliet: invalid --maxmemory-policy:", err)
		return 2
	}
	st := store.New()
	st.SetLimits(store.Limits{
		MaxMemory:      cfg.MaxMemory,
		MaxCollections: cfg.MaxCollections,
		DefaultPolicy:  policy,
	})
	go st.RunExpirer(ctx, 100*time.Millisecond)

	srv := server.New(cfg.Addr(), logger)
	command.Register(srv, st)
	command.RegisterInfo(srv, st)
	if err := srv.ListenAndServe(ctx); err != nil {
		logger.Error("server error", "err", err)
		return 1
	}
	return 0
}
