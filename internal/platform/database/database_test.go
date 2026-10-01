package database

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	sharederrors "github.com/RedeArmy/flight-intelligence-platform/internal/shared/errors"
	"github.com/RedeArmy/flight-intelligence-platform/internal/shared/secret"
)

func validConfig() Config {
	return Config{
		Host: "db.internal", Port: 5432, Name: "fip", User: "fip_app", Password: secret.Secret("pw-value-123"),
		SSLMode: SSLVerifyFull, AppName: "api", MaxConns: 10, MinConns: 1,
		ConnectTimeout: 5 * time.Second, StatementTimeout: 15 * time.Second, MaxConnLifetime: 30 * time.Minute,
	}
}

func TestConfigValidate(t *testing.T) {
	if err := validConfig().Validate(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	cases := map[string]func(*Config){
		"no host":          func(c *Config) { c.Host = "" },
		"port zero":        func(c *Config) { c.Port = 0 },
		"port too large":   func(c *Config) { c.Port = 70000 },
		"no name":          func(c *Config) { c.Name = "" },
		"no user":          func(c *Config) { c.User = "" },
		"sslmode require":  func(c *Config) { c.SSLMode = "require" },
		"sslmode empty":    func(c *Config) { c.SSLMode = "" },
		"no max conns":     func(c *Config) { c.MaxConns = 0 },
		"min above max":    func(c *Config) { c.MinConns = 11 },
		"negative min":     func(c *Config) { c.MinConns = -1 },
		"no connect limit": func(c *Config) { c.ConnectTimeout = 0 },
	}
	for name, mutate := range cases {
		c := validConfig()
		mutate(&c)
		if err := c.Validate(); err == nil {
			t.Errorf("%s: expected a validation error", name)
		}
	}
}

func TestPoolConfigIsFullyExplicitAndIgnoresTheProcessEnvironment(t *testing.T) {
	// If pgx's own PG* environment handling leaked through, these would redirect the connection.
	t.Setenv("PGHOST", "evil.example.com")
	t.Setenv("PGPORT", "6666")
	t.Setenv("PGUSER", "attacker")
	t.Setenv("PGDATABASE", "other")
	t.Setenv("PGSSLMODE", "disable")
	t.Setenv("PGPASSWORD", "env-password")

	pc, err := validConfig().poolConfig()
	if err != nil {
		t.Fatal(err)
	}
	cc := pc.ConnConfig
	if cc.Host != "db.internal" || cc.Port != 5432 || cc.Database != "fip" || cc.User != "fip_app" || cc.Password != "pw-value-123" {
		t.Fatalf("connection settings were not taken from Config: %s:%d %s %s", cc.Host, cc.Port, cc.Database, cc.User)
	}
	if len(cc.Fallbacks) != 0 {
		t.Errorf("fallbacks must be empty so there is no plaintext downgrade, got %d", len(cc.Fallbacks))
	}
	if pc.MaxConns != 10 || pc.MinConns != 1 || pc.MaxConnLifetime != 30*time.Minute {
		t.Errorf("pool sizes = %d/%d lifetime %v", pc.MinConns, pc.MaxConns, pc.MaxConnLifetime)
	}
}

func TestVerifyFullEnablesStrictTLSAndDisableTurnsItOff(t *testing.T) {
	pc, err := validConfig().poolConfig()
	if err != nil {
		t.Fatal(err)
	}
	tlsCfg := pc.ConnConfig.TLSConfig
	if tlsCfg == nil || tlsCfg.InsecureSkipVerify || tlsCfg.ServerName != "db.internal" || tlsCfg.MinVersion < tls.VersionTLS12 {
		t.Fatalf("verify-full TLS config = %+v", tlsCfg)
	}

	c := validConfig()
	c.SSLMode = SSLDisable
	pc, err = c.poolConfig()
	if err != nil {
		t.Fatal(err)
	}
	if pc.ConnConfig.TLSConfig != nil {
		t.Error("disable must not configure TLS")
	}
}

func TestSessionLimitsAreApplied(t *testing.T) {
	pc, err := validConfig().poolConfig()
	if err != nil {
		t.Fatal(err)
	}
	rp := pc.ConnConfig.RuntimeParams
	for k, want := range map[string]string{
		"application_name": "api", "statement_timeout": "15000",
		"lock_timeout": "5000", "idle_in_transaction_session_timeout": "30000",
	} {
		if rp[k] != want {
			t.Errorf("%s = %q, want %q", k, rp[k], want)
		}
	}

	c := validConfig()
	c.AppName, c.StatementTimeout = "", 0
	pc, _ = c.poolConfig()
	if pc.ConnConfig.RuntimeParams["application_name"] != "fip" {
		t.Error("application_name must default")
	}
	if _, ok := pc.ConnConfig.RuntimeParams["statement_timeout"]; ok {
		t.Error("a zero statement timeout must not be sent")
	}
}

func TestConfigNeverRevealsThePassword(t *testing.T) {
	c := validConfig()
	for name, out := range map[string]string{
		"%v": fmt.Sprintf("%v", c), "%+v": fmt.Sprintf("%+v", c), "%#v": fmt.Sprintf("%#v", c),
	} {
		if strings.Contains(out, "pw-value-123") {
			t.Errorf("%s leaked the password: %s", name, out)
		}
	}
}

func TestOpenRejectsAnInvalidConfigWithoutConnecting(t *testing.T) {
	c := validConfig()
	c.Host = ""
	if _, err := Open(context.Background(), c); err == nil {
		t.Fatal("expected an error")
	}
}

func TestOpenIsLazyAndCheckReportsAnUnreachableDatabase(t *testing.T) {
	c := validConfig()
	c.Host, c.Port, c.SSLMode, c.ConnectTimeout = "127.0.0.1", 1, SSLDisable, 200*time.Millisecond // nothing listens on port 1
	pool, err := Open(context.Background(), c)
	if err != nil {
		t.Fatalf("Open must not connect eagerly: %v", err)
	}
	defer pool.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	err = pool.Check(ctx)
	if err == nil {
		t.Fatal("Check must fail when the database is unreachable")
	}
	if sharederrors.KindOf(err) != sharederrors.KindUnavailable {
		t.Errorf("an unreachable database must be classified unavailable, got %v (%v)", sharederrors.KindOf(err), err)
	}
}

func pgError(code string) error {
	return &pgconn.PgError{Code: code, Message: "sensitive: relation api_keys"}
}

func TestClassify(t *testing.T) {
	cases := []struct {
		name string
		err  error
		kind sharederrors.Kind
		code string
	}{
		{"nil", nil, 0, ""},
		{"no rows", pgx.ErrNoRows, sharederrors.KindNotFound, sharederrors.CodeNotFound},
		{"deadline", context.DeadlineExceeded, sharederrors.KindTimeout, CodeDBTimeout},
		{"unique violation", pgError("23505"), sharederrors.KindConflict, CodeDBConflict},
		{"foreign key violation", pgError("23503"), sharederrors.KindConflict, CodeDBConflict},
		{"check violation", pgError("23514"), sharederrors.KindInvalid, CodeDBInvalidData},
		{"not null violation", pgError("23502"), sharederrors.KindInvalid, CodeDBInvalidData},
		{"data exception", pgError("22P02"), sharederrors.KindInvalid, CodeDBInvalidData},
		{"statement timeout", pgError("57014"), sharederrors.KindTimeout, CodeDBTimeout},
		{"serialization failure", pgError("40001"), sharederrors.KindUnavailable, CodeDBSerialization},
		{"deadlock", pgError("40P01"), sharederrors.KindUnavailable, CodeDBSerialization},
		{"admin shutdown", pgError("57P01"), sharederrors.KindUnavailable, CodeDBUnavailable},
		{"too many connections", pgError("53300"), sharederrors.KindUnavailable, CodeDBUnavailable},
		{"connection exception", pgError("08006"), sharederrors.KindUnavailable, CodeDBUnavailable},
		{"insufficient privilege is our bug", pgError("42501"), sharederrors.KindInternal, CodeDBPrivilege},
		{"syntax error is our bug", pgError("42601"), sharederrors.KindInternal, CodeDBError},
		{"network error", &net.OpError{Op: "dial", Err: errors.New("refused")}, sharederrors.KindUnavailable, CodeDBUnavailable},
		{"unknown", errors.New("boom"), sharederrors.KindInternal, CodeDBError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { assertClassified(t, tc.err, tc.kind, tc.code) })
	}
}

// assertClassified checks the kind and code Classify gives err, that the cause survives, and that nothing sensitive
// reaches the public message.
func assertClassified(t *testing.T, err error, kind sharederrors.Kind, code string) {
	t.Helper()
	got := Classify(err)
	if err == nil {
		if got != nil {
			t.Fatalf("Classify(nil) = %v", got)
		}
		return
	}
	if sharederrors.KindOf(got) != kind || sharederrors.CodeOf(got) != code {
		t.Fatalf("kind/code = %v/%s, want %v/%s", sharederrors.KindOf(got), sharederrors.CodeOf(got), kind, code)
	}
	if !errors.Is(got, err) {
		t.Error("the original error must stay reachable as the cause")
	}
	if strings.Contains(sharederrors.PublicMessage(got), "api_keys") {
		t.Errorf("the public message leaked database detail: %q", sharederrors.PublicMessage(got))
	}
}

func TestClassifyLeavesClassifiedErrorsAlone(t *testing.T) {
	orig := sharederrors.Forbidden("NOPE", "no")
	if got := Classify(fmt.Errorf("wrapped: %w", orig)); !errors.Is(got, orig) || sharederrors.CodeOf(got) != "NOPE" {
		t.Fatalf("Classify changed an already classified error: %v", got)
	}
}

func noWait(context.Context, time.Duration) error { return nil }

func TestRetryRerunsOnlyRetryableFailures(t *testing.T) {
	calls := 0
	err := retry(context.Background(), 3, noWait, func() error {
		calls++
		if calls < 3 {
			return pgError("40001")
		}
		return nil
	})
	if err != nil || calls != 3 {
		t.Fatalf("calls=%d err=%v; want success on the third attempt", calls, err)
	}

	calls = 0
	nonRetryable := pgError("23505")
	err = retry(context.Background(), 3, noWait, func() error { calls++; return nonRetryable })
	if !errors.Is(err, nonRetryable) || calls != 1 {
		t.Fatalf("a non-retryable error must not be retried: calls=%d err=%v", calls, err)
	}
}

func TestRetryGivesUpAfterTheMaximumAttempts(t *testing.T) {
	calls := 0
	deadlock := pgError("40P01")
	err := retry(context.Background(), 3, noWait, func() error { calls++; return deadlock })
	if !errors.Is(err, deadlock) || calls != 3 {
		t.Fatalf("calls=%d err=%v; want 3 attempts then the last error", calls, err)
	}
}

func TestRetryStopsWaitingWhenTheContextEnds(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls := 0
	err := retry(ctx, 3, waitFor, func() error { calls++; return pgError("40001") })
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("calls=%d err=%v; want one attempt then the cancellation", calls, err)
	}
}

func TestBackoffGrowsAndStaysBounded(t *testing.T) {
	for attempt := 1; attempt <= 3; attempt++ {
		for range 50 {
			d := backoff(attempt)
			lo := time.Duration(attempt) * txBaseBackoff
			if d < lo || d >= lo+txBaseBackoff {
				t.Fatalf("backoff(%d) = %v outside [%v, %v)", attempt, d, lo, lo+txBaseBackoff)
			}
		}
	}
}

func TestWaitForHonoursTheContext(t *testing.T) {
	if err := waitFor(context.Background(), time.Millisecond); err != nil {
		t.Fatalf("waitFor = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := waitFor(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatalf("waitFor on a cancelled context = %v", err)
	}
}
