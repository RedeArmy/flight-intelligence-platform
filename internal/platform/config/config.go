// Package config loads the typed, validated runtime configuration (Constitution section 70).
//
// Configuration comes from environment variables (optionally seeded from a git-ignored .env file in
// local and test environments only). Loading fails fast with every problem listed. Secrets are never
// part of Config: they are reached through the SecretStore port (ADR-018). Every key is documented in
// docs/operations/configuration.md and a test keeps code and documentation in sync.
package config

import (
	"net"
	"os"
	"time"
)

// Env is the deployment environment.
type Env string

// Environments. staging and production are "production-like": local-only features are refused there (SR-19).
const (
	EnvLocal      Env = "local"
	EnvTest       Env = "test"
	EnvStaging    Env = "staging"
	EnvProduction Env = "production"
)

// IsProductionLike reports whether e is staging or production.
func (e Env) IsProductionLike() bool { return e == EnvStaging || e == EnvProduction }

// AllowsLocalFeatures reports whether dev-only features (mock providers, local secret store,
// .env files) may be used.
func (e Env) AllowsLocalFeatures() bool { return e == EnvLocal || e == EnvTest }

// Config is the complete runtime configuration. Later E1 slices add Redis, auth and telemetry groups.
type Config struct {
	App      App
	Log      Log
	HTTP     HTTP
	Postgres Postgres
	Secrets  Secrets
}

// Postgres configures database access. Passwords are not configuration: they come from the SecretStore under the
// names postgres_password (API user) and postgres_migrator_password (migrations).
type Postgres struct {
	Host             string
	Port             int
	Name             string
	User             string // runtime role, fip_app
	MigratorUser     string // DDL role used by cmd/migrate, fip_migrator
	SSLMode          string // disable | verify-full (verify-full is required in production-like environments)
	MaxConns         int
	MinConns         int
	ConnectTimeout   time.Duration
	StatementTimeout time.Duration
	MaxConnLifetime  time.Duration
}

// Secrets configures where the local secret store looks for secret files. It is ignored by managed stores.
type Secrets struct {
	Dir string // directory of secret files, empty to use only SECRET_<NAME> environment variables
}

// App identifies the running service.
type App struct {
	Env     Env
	Version string
}

// Log configures structured logging.
type Log struct {
	Level  string // debug | info | warn | error
	Format string // json | text (text only in local and test)
}

// HTTP configures the public and operator listeners and their limits.
type HTTP struct {
	Addr              string // public API listener
	OperatorAddr      string // operator listener (metrics, admin); loopback by default
	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
	ShutdownTimeout   time.Duration
	MaxBodyBytes      int64
}

// Load reads and validates the configuration through lookup.
func Load(lookup Lookup) (Config, error) {
	p := newParser(lookup)
	cfg := build(p)
	if len(p.issues) == 0 {
		validateCross(p, cfg)
	}
	if err := p.err(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// LookupFromEnv returns a Lookup backed by the process environment.
func LookupFromEnv() Lookup { return os.LookupEnv }

// Keys lists every configuration key the loader reads, sorted. Used to keep the documentation complete.
func Keys() []string {
	p := newParser(nil)
	build(p)
	return p.keys()
}

// build reads every key. An empty default means the key is required.
func build(p *parser) Config {
	return Config{
		App: App{
			Env:     Env(p.enum("APP_ENV", "", "local", "test", "staging", "production")),
			Version: p.str("APP_VERSION", "dev"),
		},
		Log: Log{
			Level:  p.enum("LOG_LEVEL", "info", "debug", "info", "warn", "error"),
			Format: p.enum("LOG_FORMAT", "json", "json", "text"),
		},
		HTTP:     buildHTTP(p),
		Postgres: buildPostgres(p),
		Secrets:  Secrets{Dir: p.optional("SECRETS_DIR")},
	}
}

func buildHTTP(p *parser) HTTP {
	return HTTP{
		Addr:              p.addr("HTTP_ADDR", ":8080"),
		OperatorAddr:      p.addr("HTTP_OPERATOR_ADDR", "127.0.0.1:8081"),
		ReadHeaderTimeout: p.duration("HTTP_READ_HEADER_TIMEOUT", 5*time.Second),
		ReadTimeout:       p.duration("HTTP_READ_TIMEOUT", 15*time.Second),
		WriteTimeout:      p.duration("HTTP_WRITE_TIMEOUT", 30*time.Second),
		IdleTimeout:       p.duration("HTTP_IDLE_TIMEOUT", 60*time.Second),
		ShutdownTimeout:   p.duration("HTTP_SHUTDOWN_TIMEOUT", 25*time.Second),
		MaxBodyBytes:      p.int64("HTTP_MAX_BODY_BYTES", 1<<20, 1<<10, 64<<20),
	}
}

func buildPostgres(p *parser) Postgres {
	return Postgres{
		Host:             p.str("POSTGRES_HOST", "localhost"),
		Port:             int(p.int64("POSTGRES_PORT", 5432, 1, 65535)),
		Name:             p.str("POSTGRES_DB", "fip"),
		User:             p.str("POSTGRES_USER", "fip_app"),
		MigratorUser:     p.str("POSTGRES_MIGRATOR_USER", "fip_migrator"),
		SSLMode:          p.enum("POSTGRES_SSLMODE", "verify-full", "verify-full", "disable"),
		MaxConns:         int(p.int64("POSTGRES_MAX_CONNS", 10, 1, 200)),
		MinConns:         int(p.int64("POSTGRES_MIN_CONNS", 0, 0, 200)),
		ConnectTimeout:   p.duration("POSTGRES_CONNECT_TIMEOUT", 5*time.Second),
		StatementTimeout: p.duration("POSTGRES_STATEMENT_TIMEOUT", 15*time.Second),
		MaxConnLifetime:  p.duration("POSTGRES_MAX_CONN_LIFETIME", 30*time.Minute),
	}
}

// validateCross checks rules that span several keys. It runs only when every key parsed cleanly.
func validateCross(p *parser, cfg Config) {
	if cfg.Log.Format == "text" && cfg.App.Env.IsProductionLike() {
		p.fail("LOG_FORMAT", "text is allowed only in local and test environments")
	}
	if sameListener(cfg.HTTP.Addr, cfg.HTTP.OperatorAddr) {
		p.fail("HTTP_OPERATOR_ADDR", "must differ from HTTP_ADDR: operator routes must not share the public listener")
	}
	if cfg.HTTP.ReadHeaderTimeout > cfg.HTTP.ReadTimeout {
		p.fail("HTTP_READ_HEADER_TIMEOUT", "must not exceed HTTP_READ_TIMEOUT")
	}
	validatePostgres(p, cfg)
}

func validatePostgres(p *parser, cfg Config) {
	if cfg.Postgres.MinConns > cfg.Postgres.MaxConns {
		p.fail("POSTGRES_MIN_CONNS", "must not exceed POSTGRES_MAX_CONNS")
	}
	if cfg.Postgres.SSLMode != "verify-full" && cfg.App.Env.IsProductionLike() {
		p.fail("POSTGRES_SSLMODE", "must be verify-full in staging and production")
	}
	if cfg.Postgres.User == cfg.Postgres.MigratorUser {
		p.fail("POSTGRES_MIGRATOR_USER", "must differ from POSTGRES_USER: the runtime role must not be able to change the schema")
	}
}

// sameListener reports whether two listen addresses would bind the same socket. Port 0 means "any free port",
// so two port-0 addresses are different listeners.
func sameListener(a, b string) bool {
	_, portA, _ := net.SplitHostPort(a)
	return a == b && portA != "0"
}
