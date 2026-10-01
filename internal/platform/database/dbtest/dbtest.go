// Package dbtest is the integration-test harness for PostgreSQL. Each test gets its own throw-away database with the
// platform roles and (optionally) the migrations applied, so tests are isolated and can run in parallel.
//
// It needs a PostgreSQL superuser connection: TEST_POSTGRES_DSN, or by default the loopback test database started by
// `make test-db` on port 55432. When the default is not reachable the test is skipped with instructions; when
// TEST_POSTGRES_DSN is set explicitly an unreachable database fails the test (CI must not silently skip).
package dbtest

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/database"
	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/database/migrate"
	"github.com/RedeArmy/flight-intelligence-platform/internal/shared/secret"
	"github.com/RedeArmy/flight-intelligence-platform/migrations"
)

const (
	envDSN     = "TEST_POSTGRES_DSN"
	defaultDSN = "postgres://postgres@127.0.0.1:55432/postgres?sslmode=disable"

	// roleSetupLock serialises role setup across parallel test processes (roles are cluster-wide).
	roleSetupLock = 727275
)

// Role names, as created by deployments/postgres/roles.sql.
const (
	RoleMigrator = "fip_migrator"
	RoleApp      = "fip_app"
	RoleAdmin    = "fip_admin"
	RoleReadonly = "fip_readonly"
)

var allRoles = []string{RoleMigrator, RoleApp, RoleAdmin, RoleReadonly}

// Env is one isolated test database.
type Env struct {
	t    testing.TB
	host string
	port int
	// Name is the database name.
	Name  string
	super *pgx.ConnConfig
}

// New creates an isolated database with the platform roles, without migrations. The database is dropped when the
// test ends.
func New(t testing.TB) *Env {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cc := superuserConfig(t)
	admin := connect(ctx, t, cc)
	defer admin.Close(ctx)

	name := "fip_test_" + randomHex(t, 6)
	if _, err := admin.Exec(ctx, fmt.Sprintf("CREATE DATABASE %s", pgx.Identifier{name}.Sanitize())); err != nil {
		t.Fatalf("dbtest: create database: %v", err)
	}
	e := &Env{t: t, host: cc.Host, port: int(cc.Port), Name: name, super: cc}
	t.Cleanup(func() { dropDatabase(cc, name) })

	// Roles are cluster-wide but advisory locks are per database, so the lock is taken on the connection to the
	// shared admin database, which every test uses, while the new database's roles are configured.
	if _, err := admin.Exec(ctx, "SELECT pg_advisory_lock($1)", roleSetupLock); err != nil {
		t.Fatalf("dbtest: take role setup lock: %v", err)
	}
	defer func() { _, _ = admin.Exec(ctx, "SELECT pg_advisory_unlock($1)", roleSetupLock) }()
	e.setupRoles(ctx)
	return e
}

// NewMigrated is New followed by every migration, applied as the migrator role.
func NewMigrated(t testing.TB) *Env {
	t.Helper()
	e := New(t)
	if err := migrate.New(e.Config(RoleMigrator), migrations.FS, nil).Up(context.Background()); err != nil {
		t.Fatalf("dbtest: apply migrations: %v", err)
	}
	return e
}

// Config returns a connection configuration for the given role.
func (e *Env) Config(role string) database.Config {
	return database.Config{
		Host: e.host, Port: e.port, Name: e.Name, User: role, Password: password(role), SSLMode: database.SSLDisable,
		AppName: "dbtest", MaxConns: 4, ConnectTimeout: 5 * time.Second, StatementTimeout: 10 * time.Second,
	}
}

// Password returns the test password of a role (derived, never a stored literal).
func (e *Env) Password(role string) secret.Secret { return password(role) }

// Host and Port locate the database server.
func (e *Env) Host() string { return e.host }
func (e *Env) Port() int    { return e.port }

// Super opens a superuser connection to the test database. Close it when done.
func (e *Env) Super(ctx context.Context) *pgx.Conn {
	e.t.Helper()
	cc := e.super.Copy()
	cc.Database = e.Name
	return connect(ctx, e.t, cc)
}

// ExecAs runs one statement as the given role and returns the error, so tests can assert on privileges.
func (e *Env) ExecAs(ctx context.Context, role, sql string, args ...any) error {
	e.t.Helper()
	pool, err := database.Open(ctx, e.Config(role))
	if err != nil {
		e.t.Fatalf("dbtest: open pool as %s: %v", role, err)
	}
	defer pool.Close()
	_, err = pool.Exec(ctx, sql, args...)
	return err
}

// password derives a stable per-role test password so parallel test processes agree without sharing state.
func password(role string) secret.Secret {
	sum := sha256.Sum256([]byte("fip-test-role:" + role))
	return secret.Secret(hex.EncodeToString(sum[:16]))
}

func superuserConfig(t testing.TB) *pgx.ConnConfig {
	t.Helper()
	dsn, explicit := os.LookupEnv(envDSN)
	if !explicit || dsn == "" {
		dsn, explicit = defaultDSN, false
	}
	cc, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("dbtest: %s is not a valid connection string: %v", envDSN, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, err := pgx.ConnectConfig(ctx, cc)
	if err != nil {
		if explicit {
			t.Fatalf("dbtest: %s is set but the database is unreachable: %v", envDSN, err)
		}
		t.Skipf("no test database at %s (start one with `make test-db`, or set %s): %v", defaultDSN, envDSN, err)
	}
	_ = conn.Close(ctx)
	return cc
}

func connect(ctx context.Context, t testing.TB, cc *pgx.ConnConfig) *pgx.Conn {
	t.Helper()
	conn, err := pgx.ConnectConfig(ctx, cc)
	if err != nil {
		t.Fatalf("dbtest: connect: %v", err)
	}
	return conn
}

// setupRoles runs deployments/postgres/roles.sql inside the new database and sets the test passwords.
func (e *Env) setupRoles(ctx context.Context) {
	e.t.Helper()
	sql, err := os.ReadFile(rolesSQLPath(e.t))
	if err != nil {
		e.t.Fatalf("dbtest: read roles.sql: %v", err)
	}
	conn := e.Super(ctx)
	defer conn.Close(ctx)

	if _, err := conn.Exec(ctx, string(sql)); err != nil {
		e.t.Fatalf("dbtest: apply roles.sql: %v", err)
	}
	for _, role := range allRoles {
		stmt := fmt.Sprintf("ALTER ROLE %s PASSWORD '%s'", pgx.Identifier{role}.Sanitize(), password(role).Reveal())
		if _, err := conn.Exec(ctx, stmt); err != nil {
			e.t.Fatalf("dbtest: set password for %s: %v", role, err)
		}
	}
}

func dropDatabase(cc *pgx.ConnConfig, name string) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	conn, err := pgx.ConnectConfig(ctx, cc)
	if err != nil {
		return
	}
	defer conn.Close(ctx)
	_, _ = conn.Exec(ctx, fmt.Sprintf("DROP DATABASE IF EXISTS %s WITH (FORCE)", pgx.Identifier{name}.Sanitize()))
}

// rolesSQLPath finds deployments/postgres/roles.sql by walking up to the module root.
func rolesSQLPath(t testing.TB) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return filepath.Join(dir, "deployments", "postgres", "roles.sql")
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("dbtest: go.mod not found above the test directory")
		}
		dir = parent
	}
}

func randomHex(t testing.TB, n int) string {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(b)
}
