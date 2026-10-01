// Command api runs the public HTTP API and the operator listener.
//
// It is the composition root: it loads configuration, builds the logger and wires the server. Business logic
// does not live here (ADR-006).
package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/config"
	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/httpserver"
	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/observability/logging"
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

	health := httpserver.NewHealth(cfg.HTTP.ReadHeaderTimeout) // dependency checks arrive with S3 (PostgreSQL) and S4 (Redis)
	srv, err := httpserver.New(httpserver.Options{
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
		OnListening:       o.OnListening,
	})
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
