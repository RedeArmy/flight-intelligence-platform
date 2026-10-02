// Package database is the PostgreSQL platform layer: connection pool, transactions with retry, readiness check and
// error classification (ADR-003, ADR-031). It knows nothing about bounded contexts; repositories in their adapters use it.
package database

import (
	"crypto/tls"
	"errors"
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel/trace"

	"github.com/RedeArmy/flight-intelligence-platform/internal/shared/secret"
)

// SSL modes. Only modes that verify the server are offered: "require" without verification would be a false sense of
// security, so it is deliberately absent.
const (
	SSLDisable    = "disable"
	SSLVerifyFull = "verify-full"
)

// Config describes one database connection pool. Password is a secret.Secret so printing a Config never reveals it.
type Config struct {
	Host     string
	Port     int
	Name     string
	User     string
	Password secret.Secret
	SSLMode  string

	AppName          string // reported to the server as application_name
	MaxConns         int
	MinConns         int
	ConnectTimeout   time.Duration
	StatementTimeout time.Duration // 0 disables the server-side statement timeout
	MaxConnLifetime  time.Duration

	// Tracing, when set, records one client span per statement that runs under a traced request (verb only, never
	// SQL text, arguments or error messages). Nil disables it.
	Tracing trace.TracerProvider
}

// Validate reports the first problem with c.
func (c Config) Validate() error {
	switch {
	case c.Host == "":
		return errors.New("database: host is required")
	case c.Port < 1 || c.Port > 65535:
		return fmt.Errorf("database: port %d is out of range", c.Port)
	case c.Name == "" || c.User == "":
		return errors.New("database: name and user are required")
	case c.SSLMode != SSLDisable && c.SSLMode != SSLVerifyFull:
		return fmt.Errorf("database: sslmode must be %q or %q", SSLDisable, SSLVerifyFull)
	case c.MaxConns < 1 || c.MinConns < 0 || c.MinConns > c.MaxConns:
		return errors.New("database: pool sizes are invalid")
	case c.ConnectTimeout <= 0:
		return errors.New("database: connect timeout must be positive")
	}
	return nil
}

// poolConfig builds the pgx pool configuration. Every connection setting is explicit: pgx's own defaults and the
// PG* environment variables are overwritten, so the process environment can never redirect or weaken a connection.
// tracerName is the instrumentation scope of database spans.
const tracerName = "github.com/RedeArmy/flight-intelligence-platform/internal/platform/database"

func (c Config) poolConfig() (*pgxpool.Config, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	pc, err := pgxpool.ParseConfig("")
	if err != nil {
		return nil, fmt.Errorf("database: build pool configuration: %w", err)
	}

	cc := pc.ConnConfig
	cc.Host, cc.Port = c.Host, clampUint16(c.Port)
	cc.Database, cc.User, cc.Password = c.Name, c.User, c.Password.Reveal()
	cc.ConnectTimeout = c.ConnectTimeout
	cc.Fallbacks = nil // no silent downgrade to an unencrypted connection
	cc.TLSConfig = nil
	if c.SSLMode == SSLVerifyFull {
		cc.TLSConfig = &tls.Config{ServerName: c.Host, MinVersion: tls.VersionTLS12}
	}
	cc.RuntimeParams = c.runtimeParams()
	if c.Tracing != nil {
		cc.Tracer = &queryTracer{tracer: c.Tracing.Tracer(tracerName)}
	}

	pc.MaxConns = clampInt32(c.MaxConns)
	pc.MinConns = clampInt32(c.MinConns)
	pc.MaxConnLifetime = c.MaxConnLifetime
	pc.MaxConnIdleTime = 5 * time.Minute
	pc.HealthCheckPeriod = 30 * time.Second
	return pc, nil
}

// clampUint16 converts n to uint16, saturating at the type's bounds. Validate already restricts ports to 1..65535;
// the clamp makes the conversion safe on its own.
func clampUint16(n int) uint16 {
	return uint16(min(max(n, 0), math.MaxUint16))
}

// clampInt32 converts n to int32, saturating at the type's bounds.
func clampInt32(n int) int32 {
	return int32(min(max(n, math.MinInt32), math.MaxInt32))
}

// runtimeParams are applied to every session: they bound how long a statement, a lock wait or an idle transaction may
// hold resources, so a stuck client cannot exhaust the database.
func (c Config) runtimeParams() map[string]string {
	params := map[string]string{
		"application_name":                    c.AppName,
		"idle_in_transaction_session_timeout": "30000",
		"lock_timeout":                        "5000",
	}
	if params["application_name"] == "" {
		params["application_name"] = "fip"
	}
	if c.StatementTimeout > 0 {
		params["statement_timeout"] = strconv.FormatInt(c.StatementTimeout.Milliseconds(), 10)
	}
	return params
}
