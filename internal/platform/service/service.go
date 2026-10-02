// Package service is the startup every long-running process shares (the API and the worker): configuration, redacting
// logger, OpenTelemetry providers, secret store and the PostgreSQL pool. Each command's main package keeps only what is
// its own: its handlers, its extra dependencies and its listeners.
package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/cli"
	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/config"
	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/database"
	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/httpserver"
	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/observability/logging"
	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/observability/telemetry"
	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/security"
)

const (
	// DatabasePasswordSecret is the name, in the secret store, of the runtime database role's password.
	DatabasePasswordSecret = "postgres_password" // #nosec G101 -- a secret name, not a secret
	// readinessTimeout bounds each readiness check, so a hung dependency cannot hang the probe.
	readinessTimeout = 2 * time.Second
	// telemetryShutdownTimeout bounds the final flush of traces and metrics.
	telemetryShutdownTimeout = 5 * time.Second
)

// Options are the inputs of Start, injectable so the startup path is testable without a real environment.
type Options struct {
	Name       string // service name in logs and telemetry, for example "api" or "worker"
	Lookup     config.Lookup
	DotEnvPath string // "" disables the .env file
	Stdout     io.Writer
}

// Service holds what a started process needs.
type Service struct {
	Config    config.Config
	Logger    *slog.Logger
	Secrets   security.SecretGetter
	Telemetry *telemetry.Telemetry
	Inst      *httpserver.Instrumentation
	Pool      *database.Pool
}

// Start loads the configuration and builds the shared components. The database pool connects lazily, so the process
// starts while PostgreSQL is still coming up and reports not ready until it answers. On error nothing is left open.
func Start(ctx context.Context, o Options) (*Service, error) {
	cfg, lookup, err := cli.LoadConfig(cli.Options{Lookup: o.Lookup, DotEnvPath: o.DotEnvPath})
	if err != nil {
		return nil, err
	}
	logger, err := logging.New(logging.Options{
		Service: o.Name, Env: string(cfg.App.Env), Version: cfg.App.Version,
		Level: cfg.Log.Level, Format: cfg.Log.Format, Out: o.Stdout, Trace: telemetry.TraceIDs,
	})
	if err != nil {
		return nil, err
	}
	s := &Service{Config: cfg, Logger: logger}

	if s.Telemetry, s.Inst, err = newTelemetry(ctx, o.Name, cfg); err != nil {
		return nil, err
	}
	if s.Secrets, err = security.NewLocalStore(cfg.App.Env, lookup, cfg.Secrets.Dir); err != nil {
		s.shutdownTelemetry(ctx)
		return nil, err
	}
	if s.Pool, err = s.openDatabase(ctx, o.Name); err != nil {
		s.shutdownTelemetry(ctx)
		return nil, err
	}
	if err := s.registerGauges(); err != nil {
		s.Close(ctx)
		return nil, err
	}
	return s, nil
}

// Close releases the pool and flushes pending telemetry within a bounded time.
func (s *Service) Close(ctx context.Context) {
	if s.Pool != nil {
		s.Pool.Close()
	}
	s.shutdownTelemetry(ctx)
}

// Health builds the readiness checks: PostgreSQL is critical, and extra checks are added as given. It also exposes
// the readiness state as a gauge.
func (s *Service) Health(extra ...httpserver.Check) (*httpserver.Health, error) {
	checks := append([]httpserver.Check{{Name: "postgres", Critical: true, Run: s.Pool.Check}}, extra...)
	health := httpserver.NewHealth(readinessTimeout, checks...)
	err := telemetry.RegisterReadiness(s.Telemetry.Meter(), func(ctx context.Context) string {
		state, _ := health.Readiness(ctx)
		return state
	})
	if err != nil {
		return nil, err
	}
	return health, nil
}

func newTelemetry(ctx context.Context, name string, cfg config.Config) (*telemetry.Telemetry, *httpserver.Instrumentation, error) {
	tel, err := telemetry.New(ctx, telemetry.Options{
		Service: name, Version: cfg.App.Version, Env: string(cfg.App.Env),
		Endpoint: cfg.Telemetry.Endpoint, SampleRatio: cfg.Telemetry.SampleRatio, MetricInterval: cfg.Telemetry.MetricInterval,
	})
	if err != nil {
		return nil, nil, err
	}
	metrics, err := telemetry.NewMetrics(tel.Meter())
	if err != nil {
		return nil, nil, errors.Join(err, shutdown(ctx, tel))
	}
	return tel, &httpserver.Instrumentation{Tracer: tel.Tracer(), Propagator: telemetry.Propagator(), Metrics: metrics}, nil
}

// openDatabase reads the runtime password from the secret store and opens the pool for the named workload.
func (s *Service) openDatabase(ctx context.Context, name string) (*database.Pool, error) {
	password, err := s.Secrets.Get(ctx, DatabasePasswordSecret)
	if err != nil {
		return nil, fmt.Errorf("database password: %w", err)
	}
	cfg := database.FromConfig(s.Config.Postgres, s.Config.Postgres.User, password, name)
	cfg.Tracing = s.Telemetry.TracerProvider()
	return database.Open(ctx, cfg)
}

// registerGauges exposes pool saturation and Go runtime statistics.
func (s *Service) registerGauges() error {
	meter := s.Telemetry.Meter()
	err := telemetry.RegisterPool(meter, func() telemetry.PoolStats {
		st := s.Pool.Stat()
		return telemetry.PoolStats{
			Acquired: st.AcquiredConns(), Idle: st.IdleConns(), Total: st.TotalConns(), Max: st.MaxConns(), EmptyAcquires: st.EmptyAcquireCount(),
		}
	})
	if err != nil {
		return err
	}
	return telemetry.RegisterRuntime(meter)
}

// shutdownTelemetry flushes pending telemetry. The parent is already cancelled when the process is stopping; the
// flush still needs a live context, and it is bounded so an unreachable collector cannot delay the exit.
func (s *Service) shutdownTelemetry(parent context.Context) {
	if s.Telemetry == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), telemetryShutdownTimeout)
	defer cancel()
	if err := s.Telemetry.Shutdown(ctx); err != nil && s.Logger != nil {
		s.Logger.WarnContext(ctx, "telemetry shutdown incomplete", "error", err)
	}
}

func shutdown(parent context.Context, tel *telemetry.Telemetry) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), telemetryShutdownTimeout)
	defer cancel()
	return tel.Shutdown(ctx)
}
