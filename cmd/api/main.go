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

	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/apiauth"
	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/cache"
	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/config"
	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/database"
	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/httpserver"
	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/observability/logging"
	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/ratelimit"
	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/security"
	"github.com/RedeArmy/flight-intelligence-platform/internal/shared/clock"
	sharederrors "github.com/RedeArmy/flight-intelligence-platform/internal/shared/errors"
)

const (
	appPasswordSecret = "postgres_password"
	pepperSecret      = "api_key_pepper" // #nosec G101 -- a secret name, not a secret
	// redisPasswordSecret is optional: a local Redis has no password.
	redisPasswordSecret = "redis_password" // #nosec G101 -- a secret name, not a secret
	// fallbackRetry is how long the limiter keeps using local limits after Redis fails before trying Redis again.
	fallbackRetry = 5 * time.Second
	// readinessTimeout bounds each readiness check, so a hung dependency cannot hang the probe.
	readinessTimeout = 2 * time.Second
)

// main only wires the operating system: signals, the real environment and the process exit code.
func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := realMain(ctx, runOptions{Lookup: config.LookupFromEnv(), DotEnvPath: ".env", Stdout: os.Stdout}, os.Stderr)
	stop()
	os.Exit(code)
}

// realMain runs the API and turns its result into an exit code: 0 after a clean stop, 1 with the error on stderr.
func realMain(ctx context.Context, o runOptions, stderr io.Writer) int {
	if err := run(ctx, o); err != nil {
		fmt.Fprintln(stderr, "api:", err)
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

	store, err := security.NewLocalStore(cfg.App.Env, lookup, cfg.Secrets.Dir)
	if err != nil {
		return err
	}
	pool, err := openDatabase(ctx, cfg, store)
	if err != nil {
		return err
	}
	defer pool.Close()

	auth, err := newAuthenticator(ctx, store, pool, logger)
	if err != nil {
		return err
	}
	lim, err := newLimiting(ctx, cfg, store, logger)
	if err != nil {
		return err
	}
	defer lim.release()

	srv, err := newServer(cfg, logger, serverDeps{
		pool: pool, auth: auth, limiting: lim, auditor: apiauth.NewPGAuditor(pool, clock.System{}),
	}, o.OnListening)
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
func openDatabase(ctx context.Context, cfg config.Config, store security.SecretGetter) (*database.Pool, error) {
	password, err := store.Get(ctx, appPasswordSecret)
	if err != nil {
		return nil, fmt.Errorf("database password: %w", err)
	}
	return database.Open(ctx, database.FromConfig(cfg.Postgres, cfg.Postgres.User, password, "api"))
}

// newAuthenticator builds the API-key authenticator. The pepper comes from the secret store; without it the API
// refuses to start, because an authenticator that cannot verify keys would only ever deny.
func newAuthenticator(ctx context.Context, store security.SecretGetter, pool *database.Pool, logger *slog.Logger) (*apiauth.Authenticator, error) {
	pepper, err := store.Get(ctx, pepperSecret)
	if err != nil {
		return nil, fmt.Errorf("API key pepper: %w", err)
	}
	hasher, err := security.NewKeyHasher(pepper)
	if err != nil {
		return nil, err
	}
	return apiauth.New(apiauth.NewPGStore(pool), hasher, clock.System{}, logger), nil
}

// limiting is the rate limiter and, when Redis is configured, its optional readiness check.
type limiting struct {
	limiter httpserver.RateLimiter
	checks  []httpserver.Check
	close   func() // releases the Redis connection; nil when there is none
}

// release frees what the limiter holds, if anything.
func (l limiting) release() {
	if l.close != nil {
		l.close()
	}
}

// newLimiting builds the rate limiter. Without REDIS_ADDR it limits per instance in memory. With Redis, counters are
// shared across instances and a local limiter with stricter limits takes over if Redis fails (ADR-004, ADR-032).
func newLimiting(ctx context.Context, cfg config.Config, store security.SecretGetter, logger *slog.Logger) (limiting, error) {
	local := ratelimit.NewMemory(clock.System{}, ratelimit.DefaultMaxKeys)
	if cfg.Redis.Addr == "" {
		logger.WarnContext(ctx, "REDIS_ADDR is not set: rate limits apply per instance only")
		return limiting{limiter: local}, nil
	}
	password, err := store.Get(ctx, redisPasswordSecret)
	if err != nil && sharederrors.CodeOf(err) != security.CodeSecretNotFound {
		return limiting{}, fmt.Errorf("redis password: %w", err)
	}
	rc, err := cache.Open(cfg.Redis, password)
	if err != nil {
		return limiting{}, err
	}
	return limiting{
		limiter: ratelimit.NewFallback(ratelimit.NewRedis(rc.Scripter()), local, clock.System{}, logger, ratelimit.DefaultScale, fallbackRetry),
		checks:  []httpserver.Check{{Name: "redis", Critical: false, Run: rc.Check}},
		close:   func() { _ = rc.Close() },
	}, nil
}

// serverDeps are the built components newServer wires together.
type serverDeps struct {
	pool     *database.Pool
	auth     httpserver.Authenticator
	limiting limiting
	auditor  httpserver.Auditor
}

func newServer(cfg config.Config, logger *slog.Logger, d serverDeps, onListening func(public, operator net.Addr)) (*httpserver.Server, error) {
	checks := append([]httpserver.Check{{Name: "postgres", Critical: true, Run: d.pool.Check}}, d.limiting.checks...)
	health := httpserver.NewHealth(readinessTimeout, checks...)
	return httpserver.New(httpserver.Options{
		Logger: logger,
		Public: httpserver.NewPublicHandler(httpserver.PublicDeps{
			Logger:       logger,
			Auth:         d.auth,
			Limiter:      d.limiting.limiter,
			Limits:       limitsFrom(cfg.Limits),
			Auditor:      d.auditor,
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

// limitsFrom turns the per-minute settings into token-bucket rules.
func limitsFrom(l config.RateLimits) httpserver.Limits {
	perMinute := func(n int) ratelimit.Rule { return ratelimit.Rule{Limit: n, Window: time.Minute} }
	return httpserver.Limits{
		IP:          perMinute(l.IPPerMinute),
		Client:      perMinute(l.ClientPerMinute),
		AuthFailure: perMinute(l.AuthFailuresPerMinute),
	}
}
