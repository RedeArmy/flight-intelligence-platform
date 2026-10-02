// Command worker runs the background worker process (ADR-012).
//
// It has the same lifecycle as the API: it starts the shared services (configuration, logger, telemetry, database
// pool), serves liveness and readiness on a small loopback listener, and on SIGINT or SIGTERM reports not ready,
// stops accepting probes, and exits cleanly. No job handlers are registered yet: the PostgreSQL queue adapter and the
// first handlers arrive with slice E8, against the contract in internal/platform/queue. Until then the process idles,
// which is what lets the image, the Compose service and the health checks be built and proven now.
package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/cli"
	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/config"
	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/httpserver"
	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/service"
)

const (
	// healthAddrKey and defaultHealthAddr name the worker's probe listener; the health check reads the same key.
	healthAddrKey     = "WORKER_HEALTH_ADDR"
	defaultHealthAddr = "127.0.0.1:8082"
	serviceName       = "worker"
)

// main only wires the operating system: signals, the real environment and the process exit code.
func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	if len(os.Args) > 1 && os.Args[1] == cli.HealthcheckCommand {
		code := cli.Healthcheck(ctx, config.LookupFromEnv(), healthAddrKey, defaultHealthAddr, http.DefaultClient, os.Stderr)
		stop()
		os.Exit(code)
	}
	code := realMain(ctx, runOptions{Lookup: config.LookupFromEnv(), DotEnvPath: ".env", Stdout: os.Stdout}, os.Stderr)
	stop()
	os.Exit(code)
}

// realMain runs the worker and turns its result into an exit code: 0 after a clean stop, 1 with the error on stderr.
func realMain(ctx context.Context, o runOptions, stderr io.Writer) int {
	if err := run(ctx, o); err != nil {
		fmt.Fprintln(stderr, serviceName+":", err)
		return 1
	}
	return 0
}

// runOptions are the inputs of run, injectable so the startup path is testable without a real environment.
type runOptions struct {
	Lookup      config.Lookup
	DotEnvPath  string // "" disables the .env file
	Stdout      io.Writer
	OnListening func(health net.Addr)
}

// run starts the worker and blocks until ctx is cancelled or the probe listener fails.
func run(ctx context.Context, o runOptions) error {
	svc, err := service.Start(ctx, service.Options{Name: serviceName, Lookup: o.Lookup, DotEnvPath: o.DotEnvPath, Stdout: o.Stdout})
	if err != nil {
		return err
	}
	defer svc.Close(ctx)

	health, err := svc.Health()
	if err != nil {
		return err
	}
	cfg := svc.Config
	srv, err := httpserver.New(httpserver.Options{
		Logger:            svc.Logger,
		Public:            httpserver.NewHealthHandler(httpserver.HealthDeps{Logger: svc.Logger, Health: health, Telemetry: svc.Inst}),
		Health:            health,
		PublicAddr:        cfg.Worker.HealthAddr,
		ReadHeaderTimeout: cfg.HTTP.ReadHeaderTimeout,
		ReadTimeout:       cfg.HTTP.ReadTimeout,
		WriteTimeout:      cfg.HTTP.WriteTimeout,
		IdleTimeout:       cfg.HTTP.IdleTimeout,
		ShutdownTimeout:   cfg.HTTP.ShutdownTimeout,
		OnListening: func(public, _ net.Addr) {
			if o.OnListening != nil {
				o.OnListening(public)
			}
		},
	})
	if err != nil {
		return err
	}
	svc.Logger.InfoContext(ctx, "worker starting", "job_kinds", 0)
	if err := srv.Run(ctx); err != nil {
		return err
	}
	svc.Logger.InfoContext(ctx, "worker stopped")
	return nil
}
