// Command api runs the public HTTP API and the operator listener.
//
// It is the composition root: it starts the shared services (configuration, logger, telemetry, database pool), builds
// what only the API needs (authentication, rate limiting, audit) and wires the server. Business logic does not live
// here (ADR-006).
package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/apiauth"
	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/cache"
	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/cli"
	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/config"
	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/httpserver"
	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/ratelimit"
	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/security"
	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/service"
	"github.com/RedeArmy/flight-intelligence-platform/internal/shared/clock"
	sharederrors "github.com/RedeArmy/flight-intelligence-platform/internal/shared/errors"
)

const (
	pepperSecret = "api_key_pepper" // #nosec G101 -- a secret name, not a secret
	// redisPasswordSecret is optional: a local Redis has no password.
	redisPasswordSecret = "redis_password" // #nosec G101 -- a secret name, not a secret
	// fallbackRetry is how long the limiter keeps using local limits after Redis fails before trying Redis again.
	fallbackRetry = 5 * time.Second
)

// main only wires the operating system: signals, the real environment and the process exit code.
func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	if len(os.Args) > 1 && os.Args[1] == cli.HealthcheckCommand {
		code := cli.Healthcheck(ctx, config.LookupFromEnv(), "HTTP_ADDR", ":8080", http.DefaultClient, os.Stderr)
		stop()
		os.Exit(code)
	}
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
	svc, err := service.Start(ctx, service.Options{Name: "api", Lookup: o.Lookup, DotEnvPath: o.DotEnvPath, Stdout: o.Stdout})
	if err != nil {
		return err
	}
	defer svc.Close(ctx)

	auth, err := newAuthenticator(ctx, svc)
	if err != nil {
		return err
	}
	lim, err := newLimiting(ctx, svc.Config, svc.Secrets, svc.Logger)
	if err != nil {
		return err
	}
	defer lim.release()

	srv, err := newServer(svc, serverDeps{
		auth: auth, limiting: lim, auditor: apiauth.NewPGAuditor(svc.Pool, clock.System{}),
	}, o.OnListening)
	if err != nil {
		return err
	}
	svc.Logger.InfoContext(ctx, "api starting")
	if err := srv.Run(ctx); err != nil {
		return err
	}
	svc.Logger.InfoContext(ctx, "api stopped")
	return nil
}

// newAuthenticator builds the API-key authenticator. The pepper comes from the secret store; without it the API
// refuses to start, because an authenticator that cannot verify keys would only ever deny.
func newAuthenticator(ctx context.Context, svc *service.Service) (*apiauth.Authenticator, error) {
	pepper, err := svc.Secrets.Get(ctx, pepperSecret)
	if err != nil {
		return nil, fmt.Errorf("API key pepper: %w", err)
	}
	hasher, err := security.NewKeyHasher(pepper)
	if err != nil {
		return nil, err
	}
	auth := apiauth.New(apiauth.NewPGStore(svc.Pool), hasher, clock.System{}, svc.Logger)
	return auth.WithFailureObserver(svc.Inst.Metrics.AuthFailure), nil
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

// serverDeps are the API-specific components newServer wires together.
type serverDeps struct {
	auth     httpserver.Authenticator
	limiting limiting
	auditor  httpserver.Auditor
}

func newServer(svc *service.Service, d serverDeps, onListening func(public, operator net.Addr)) (*httpserver.Server, error) {
	cfg := svc.Config
	health, err := svc.Health(d.limiting.checks...)
	if err != nil {
		return nil, err
	}
	return httpserver.New(httpserver.Options{
		Logger: svc.Logger,
		Public: httpserver.NewPublicHandler(httpserver.PublicDeps{
			Logger:       svc.Logger,
			Auth:         d.auth,
			Limiter:      d.limiting.limiter,
			Limits:       limitsFrom(cfg.Limits),
			Auditor:      d.auditor,
			Telemetry:    svc.Inst,
			Health:       health,
			MaxBodyBytes: cfg.HTTP.MaxBodyBytes,
		}),
		Operator:          httpserver.NewOperatorHandler(httpserver.OperatorDeps{Logger: svc.Logger, Telemetry: svc.Inst}),
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
