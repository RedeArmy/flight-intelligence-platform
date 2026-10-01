// Command api runs the public HTTP API and the operator listener.
//
// It is the composition root: it loads configuration, builds the logger, opens the database pool and wires the server.
// Business logic does not live here (ADR-006).
package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/config"
	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/database"
	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/httpserver"
	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/observability/logging"
	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/security"
)

const (
	appPasswordSecret = "postgres_password"
	// readinessTimeout bounds each readiness check, so a hung dependency cannot hang the probe.
	readinessTimeout = 2 * time.Second
)

func main() {
	os.Exit(realMain())
}

func realMain() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	err := run(ctx, runOptions{Lookup: config.LookupFromEnv(), DotEnvPath: ".env", Stdout: os.Stdout})
	if err != nil {
		fmt.Fprintln(os.Stderr, "api:", err)
		return 1
	}
	return 0
}

// runOptions are the inputs of run, injectable so the startup path is testable without a real environment.
type runOptions struct {
	Lookup      config.Lookup
	DotEnvPath  string // "" disables the .env file
	Stdout      io.Writer
	OnListening func(public, operator net.Addr)
}

// run starts the API and blocks until ctx is cancelled or a listener fails.
func run(ctx context.Context, o runOptions) error {
	lookup, err := config.LookupWithDotEnv(o.DotEnvPath, o.Lookup)
	if err != nil {
		return err
	}
	cfg, err := config.Load(lookup)
	if err != nil {
		return err
	}
	logger, err := logging.New(logging.Options{
		Service: "api",
		Env:     string(cfg.App.Env),
		Version: cfg.App.Version,
		Level:   cfg.Log.Level,
		Format:  cfg.Log.Format,
		Out:     o.Stdout,
	})
	if err != nil {
		return err
	}

	pool, err := openDatabase(ctx, cfg, lookup)
	if err != nil {
		return err
	}
	defer pool.Close()

	srv, err := newServer(cfg, logger, pool, o.OnListening)
	if err != nil {
		return err
	}
	logger.InfoContext(ctx, "api starting", "env", string(cfg.App.Env))
	if err := srv.Run(ctx); err != nil {
		return err
	}
	logger.InfoContext(ctx, "api stopped")
	return nil
}

// openDatabase reads the runtime password from the secret store and opens the pool. The pool connects lazily: the API
// starts even if PostgreSQL is still coming up and reports not ready until it answers.
func openDatabase(ctx context.Context, cfg config.Config, lookup config.Lookup) (*database.Pool, error) {
	store, err := security.NewLocalStore(cfg.App.Env, lookup, cfg.Secrets.Dir)
	if err != nil {
		return nil, err
	}
	password, err := store.Get(ctx, appPasswordSecret)
	if err != nil {
		return nil, fmt.Errorf("database password: %w", err)
	}
	return database.Open(ctx, database.FromConfig(cfg.Postgres, cfg.Postgres.User, password, "api"))
}

func newServer(cfg config.Config, logger *slog.Logger, pool *database.Pool, onListening func(public, operator net.Addr)) (*httpserver.Server, error) {
	health := httpserver.NewHealth(readinessTimeout,
		httpserver.Check{Name: "postgres", Critical: true, Run: pool.Check},
	)
	return httpserver.New(httpserver.Options{
		Logger: logger,
		Public: httpserver.NewPublicHandler(httpserver.PublicDeps{
			Logger:       logger,
			Auth:         nil, // DenyAll until the API-key authenticator lands in S4
			Health:       health,
			MaxBodyBytes: cfg.HTTP.MaxBodyBytes,
		}),
		Operator:          httpserver.NewOperatorHandler(httpserver.OperatorDeps{Logger: logger}),
		Health:            health,
		PublicAddr:        cfg.HTTP.Addr,
		OperatorAddr:      cfg.HTTP.OperatorAddr,
		ReadHeaderTimeout: cfg.HTTP.ReadHeaderTimeout,
		ReadTimeout:       cfg.HTTP.ReadTimeout,
		WriteTimeout:      cfg.HTTP.WriteTimeout,
		IdleTimeout:       cfg.HTTP.IdleTimeout,
		ShutdownTimeout:   cfg.HTTP.ShutdownTimeout,
		OnListening:       onListening,
	})
}
