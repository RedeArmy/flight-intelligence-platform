//go:build integration

package database_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/database"
	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/database/dbtest"
	sharederrors "github.com/RedeArmy/flight-intelligence-platform/internal/shared/errors"
)

// fixtures inserts one client, one key and one audit row as superuser, bypassing every grant.
func fixtures(ctx context.Context, t *testing.T, env *dbtest.Env) {
	t.Helper()
	conn := env.Super(ctx)
	defer conn.Close(ctx)
	for _, stmt := range []string{
		`INSERT INTO api_clients (id, name, role) VALUES ('11111111-1111-1111-1111-111111111111', 'fixture-client', 'DEVELOPER')`,
		`INSERT INTO api_keys (id, client_id, prefix, secret_hmac) VALUES ('22222222-2222-2222-2222-222222222222',
		   '11111111-1111-1111-1111-111111111111', 'abcd1234', decode(repeat('ab', 32), 'hex'))`,
		`INSERT INTO audit_events (id, action, outcome) VALUES ('33333333-3333-3333-3333-333333333333', 'fixture.created', 'success')`,
	} {
		if _, err := conn.Exec(ctx, stmt); err != nil {
			t.Fatalf("fixture: %v", err)
		}
	}
}

// TestPrivilegeMatrix proves, against a real database, that every role can do exactly what the design allows and
// nothing more (SR-15, SR-24, INV-3). A denied statement must fail with a privilege error.
func TestPrivilegeMatrix(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	env := dbtest.NewMigrated(t)
	fixtures(ctx, t, env)

	const (
		allowed = true
		denied  = false
	)
	cases := []struct {
		role, name, sql string
		want            bool
	}{
		// fip_app: the runtime API. Reads clients and keys, touches only last_used_at, appends audit events.
		{dbtest.RoleApp, "app reads clients", `SELECT id, name, role, active FROM api_clients`, allowed},
		{dbtest.RoleApp, "app reads keys including the hmac", `SELECT prefix, secret_hmac, revoked_at FROM api_keys`, allowed},
		{dbtest.RoleApp, "app records key usage", `UPDATE api_keys SET last_used_at = now()`, allowed},
		{dbtest.RoleApp, "app appends audit events", `INSERT INTO audit_events (id, action, outcome) VALUES (gen_random_uuid(), 'auth.failed', 'denied')`, allowed},
		{dbtest.RoleApp, "app cannot revoke keys", `UPDATE api_keys SET revoked_at = now()`, denied},
		{dbtest.RoleApp, "app cannot change key material", `UPDATE api_keys SET secret_hmac = decode(repeat('cd', 32), 'hex')`, denied},
		{dbtest.RoleApp, "app cannot create keys", `INSERT INTO api_keys (id, client_id, prefix, secret_hmac) VALUES (gen_random_uuid(), '11111111-1111-1111-1111-111111111111', 'zzzz9999', decode(repeat('ab', 32), 'hex'))`, denied},
		{dbtest.RoleApp, "app cannot create clients", `INSERT INTO api_clients (id, name, role) VALUES (gen_random_uuid(), 'rogue', 'ADMIN')`, denied},
		{dbtest.RoleApp, "app cannot promote a client", `UPDATE api_clients SET role = 'ADMIN'`, denied},
		{dbtest.RoleApp, "app cannot delete keys", `DELETE FROM api_keys`, denied},
		{dbtest.RoleApp, "app cannot read the audit log", `SELECT * FROM audit_events`, denied},
		{dbtest.RoleApp, "app cannot rewrite the audit log", `UPDATE audit_events SET outcome = 'success'`, denied},
		{dbtest.RoleApp, "app cannot erase the audit log", `DELETE FROM audit_events`, denied},
		{dbtest.RoleApp, "app cannot truncate the audit log", `TRUNCATE audit_events`, denied},
		{dbtest.RoleApp, "app cannot create tables", `CREATE TABLE app_made_this (id int)`, denied},

		// fip_admin: operator tooling (cmd/keyctl).
		{dbtest.RoleAdmin, "admin creates clients", `INSERT INTO api_clients (id, name, role) VALUES (gen_random_uuid(), 'new-client', 'SERVICE')`, allowed},
		{dbtest.RoleAdmin, "admin creates keys", `INSERT INTO api_keys (id, client_id, prefix, secret_hmac) VALUES (gen_random_uuid(), '11111111-1111-1111-1111-111111111111', 'wxyz0000', decode(repeat('ab', 32), 'hex'))`, allowed},
		{dbtest.RoleAdmin, "admin revokes keys", `UPDATE api_keys SET revoked_at = now()`, allowed},
		{dbtest.RoleAdmin, "admin appends and reads audit", `INSERT INTO audit_events (id, action, outcome) VALUES (gen_random_uuid(), 'key.revoked', 'success')`, allowed},
		{dbtest.RoleAdmin, "admin reads the audit log", `SELECT * FROM audit_events`, allowed},
		{dbtest.RoleAdmin, "admin cannot change key material", `UPDATE api_keys SET secret_hmac = decode(repeat('cd', 32), 'hex')`, denied},
		{dbtest.RoleAdmin, "admin cannot delete keys", `DELETE FROM api_keys`, denied},
		{dbtest.RoleAdmin, "admin cannot rewrite the audit log", `UPDATE audit_events SET outcome = 'failure'`, denied},
		{dbtest.RoleAdmin, "admin cannot erase the audit log", `DELETE FROM audit_events`, denied},
		{dbtest.RoleAdmin, "admin cannot create tables", `CREATE TABLE admin_made_this (id int)`, denied},

		// fip_readonly: analysts and support, never key material.
		{dbtest.RoleReadonly, "readonly reads clients", `SELECT * FROM api_clients`, allowed},
		{dbtest.RoleReadonly, "readonly reads key metadata", `SELECT id, client_id, prefix, created_at, expires_at, revoked_at, last_used_at FROM api_keys`, allowed},
		{dbtest.RoleReadonly, "readonly reads the audit log", `SELECT * FROM audit_events`, allowed},
		{dbtest.RoleReadonly, "readonly cannot read the hmac", `SELECT secret_hmac FROM api_keys`, denied},
		{dbtest.RoleReadonly, "readonly cannot SELECT * from keys", `SELECT * FROM api_keys`, denied},
		{dbtest.RoleReadonly, "readonly cannot write clients", `UPDATE api_clients SET active = false`, denied},
		{dbtest.RoleReadonly, "readonly cannot append audit", `INSERT INTO audit_events (id, action, outcome) VALUES (gen_random_uuid(), 'x.y', 'success')`, denied},

		// fip_migrator owns the schema objects and is the only role that can change the schema.
		{dbtest.RoleMigrator, "migrator can change the schema", `CREATE TABLE migrator_scratch (id int)`, allowed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := env.ExecAs(ctx, tc.role, tc.sql)
			switch {
			case tc.want && err != nil:
				t.Fatalf("%s should be allowed, got: %v", tc.role, err)
			case !tc.want && err == nil:
				t.Fatalf("%s must be denied, but the statement succeeded", tc.role)
			case !tc.want && sharederrors.CodeOf(err) != database.CodeDBPrivilege:
				t.Fatalf("%s denied with the wrong error: %v", tc.role, err)
			}
		})
	}
}

func TestNoRoleCanReachTheDatabaseWithoutBeingGrantedConnect(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	env := dbtest.NewMigrated(t)
	super := env.Super(ctx)
	defer super.Close(ctx)

	// A role that exists but has no explicit CONNECT grant must be refused: PUBLIC was revoked.
	if _, err := super.Exec(ctx, `CREATE ROLE stranger LOGIN PASSWORD 'stranger-pass-1'`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		c := env.Super(context.Background())
		defer c.Close(context.Background())
		_, _ = c.Exec(context.Background(), `DROP ROLE IF EXISTS stranger`)
	})
	cfg := env.Config(dbtest.RoleApp)
	cfg.User, cfg.Password = "stranger", "stranger-pass-1"
	pool, err := database.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := pool.Check(ctx); err == nil {
		t.Fatal("a role without a CONNECT grant must not be able to connect")
	}
}

func TestTableConstraintsRejectBadData(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	env := dbtest.NewMigrated(t)
	fixtures(ctx, t, env)
	conn := env.Super(ctx)
	defer conn.Close(ctx)

	cases := []struct {
		name string
		sql  string
		kind sharederrors.Kind
		code string
	}{
		{"duplicate client name", `INSERT INTO api_clients (id, name, role) VALUES (gen_random_uuid(), 'fixture-client', 'USER')`, sharederrors.KindConflict, database.CodeDBConflict},
		{"unknown role value", `INSERT INTO api_clients (id, name, role) VALUES (gen_random_uuid(), 'x1', 'ROOT')`, sharederrors.KindInvalid, database.CodeDBInvalidData},
		{"empty client name", `INSERT INTO api_clients (id, name, role) VALUES (gen_random_uuid(), '', 'USER')`, sharederrors.KindInvalid, database.CodeDBInvalidData},
		{"duplicate key prefix", `INSERT INTO api_keys (id, client_id, prefix, secret_hmac) VALUES (gen_random_uuid(), '11111111-1111-1111-1111-111111111111', 'abcd1234', decode(repeat('ab', 32), 'hex'))`, sharederrors.KindConflict, database.CodeDBConflict},
		{"malformed key prefix", `INSERT INTO api_keys (id, client_id, prefix, secret_hmac) VALUES (gen_random_uuid(), '11111111-1111-1111-1111-111111111111', 'short', decode(repeat('ab', 32), 'hex'))`, sharederrors.KindInvalid, database.CodeDBInvalidData},
		{"wrong hmac length", `INSERT INTO api_keys (id, client_id, prefix, secret_hmac) VALUES (gen_random_uuid(), '11111111-1111-1111-1111-111111111111', 'qqqq1111', decode('abcd', 'hex'))`, sharederrors.KindInvalid, database.CodeDBInvalidData},
		{"key for a missing client", `INSERT INTO api_keys (id, client_id, prefix, secret_hmac) VALUES (gen_random_uuid(), gen_random_uuid(), 'rrrr2222', decode(repeat('ab', 32), 'hex'))`, sharederrors.KindConflict, database.CodeDBConflict},
		{"expiry before creation", `INSERT INTO api_keys (id, client_id, prefix, secret_hmac, expires_at) VALUES (gen_random_uuid(), '11111111-1111-1111-1111-111111111111', 'ssss3333', decode(repeat('ab', 32), 'hex'), now() - interval '1 day')`, sharederrors.KindInvalid, database.CodeDBInvalidData},
		{"bad audit action", `INSERT INTO audit_events (id, action, outcome) VALUES (gen_random_uuid(), 'Bad Action!', 'success')`, sharederrors.KindInvalid, database.CodeDBInvalidData},
		{"bad audit outcome", `INSERT INTO audit_events (id, action, outcome) VALUES (gen_random_uuid(), 'a.b', 'maybe')`, sharederrors.KindInvalid, database.CodeDBInvalidData},
		{"audit details must be an object", `INSERT INTO audit_events (id, action, outcome, details) VALUES (gen_random_uuid(), 'a.b', 'success', '[1]')`, sharederrors.KindInvalid, database.CodeDBInvalidData},
		{"cannot delete a client that owns keys", `DELETE FROM api_clients`, sharederrors.KindConflict, database.CodeDBConflict},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := conn.Exec(ctx, tc.sql)
			got := database.Classify(err)
			if err == nil || sharederrors.KindOf(got) != tc.kind || sharederrors.CodeOf(got) != tc.code {
				t.Fatalf("err=%v classified as %v/%s, want %v/%s", err, sharederrors.KindOf(got), sharederrors.CodeOf(got), tc.kind, tc.code)
			}
		})
	}
}

func TestRolesScriptIsIdempotent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	env := dbtest.NewMigrated(t)
	sql, err := os.ReadFile(filepath.Join("..", "..", "..", "deployments", "postgres", "roles.sql"))
	if err != nil {
		t.Fatal(err)
	}
	conn := env.Super(ctx)
	defer conn.Close(ctx)
	for run := 1; run <= 2; run++ {
		if _, err := conn.Exec(ctx, string(sql)); err != nil {
			t.Fatalf("run %d of roles.sql failed: %v", run, err)
		}
	}
	// Re-running must not widen what the application role can do.
	if err := env.ExecAs(ctx, dbtest.RoleApp, `CREATE TABLE still_denied (id int)`); err == nil {
		t.Fatal("app role can create tables after re-running roles.sql")
	}
}

func openPool(t *testing.T, cfg database.Config) *database.Pool {
	t.Helper()
	pool, err := database.Open(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func TestPoolCheckAndSessionLimits(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	env := dbtest.NewMigrated(t)
	pool := openPool(t, env.Config(dbtest.RoleApp))

	if err := pool.Check(ctx); err != nil {
		t.Fatalf("Check against a healthy database: %v", err)
	}
	var user, app, statementTimeout string
	row := pool.QueryRow(ctx, `SELECT current_user, current_setting('application_name'), current_setting('statement_timeout')`)
	if err := row.Scan(&user, &app, &statementTimeout); err != nil {
		t.Fatal(err)
	}
	if user != dbtest.RoleApp || app != "dbtest" || statementTimeout != "10s" {
		t.Errorf("session = %s/%s/%s, want fip_app/dbtest/10s", user, app, statementTimeout)
	}
}

func TestStatementTimeoutIsEnforcedAndClassified(t *testing.T) {
	t.Parallel()
	env := dbtest.NewMigrated(t)
	cfg := env.Config(dbtest.RoleApp)
	cfg.StatementTimeout = 100 * time.Millisecond
	pool := openPool(t, cfg)

	start := time.Now()
	_, err := pool.Exec(context.Background(), `SELECT pg_sleep(5)`)
	if sharederrors.KindOf(err) != sharederrors.KindTimeout || sharederrors.CodeOf(err) != database.CodeDBTimeout {
		t.Fatalf("a slow statement must time out: %v", err)
	}
	if time.Since(start) > 3*time.Second {
		t.Errorf("the statement ran for %v instead of being cut off", time.Since(start))
	}
}

func TestTransactionsCommitAndRollBack(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	env := dbtest.NewMigrated(t)
	pool := openPool(t, env.Config(dbtest.RoleAdmin))
	count := func() int {
		super := env.Super(ctx)
		defer super.Close(ctx)
		var n int
		if err := super.QueryRow(ctx, `SELECT count(*) FROM audit_events`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	insert := func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO audit_events (id, action, outcome) VALUES (gen_random_uuid(), 'tx.test', 'success')`)
		return err
	}

	if err := pool.WithTx(ctx, pgx.TxOptions{}, insert); err != nil || count() != 1 {
		t.Fatalf("commit: err=%v rows=%d", err, count())
	}

	boom := errors.New("business rule failed")
	err := pool.WithTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := insert(tx); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) || count() != 1 {
		t.Fatalf("rollback: err=%v rows=%d (want the error and still 1 row)", err, count())
	}
}

func TestTransactionRetriesSerializationFailuresThenCommitsOnce(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	env := dbtest.NewMigrated(t)
	pool := openPool(t, env.Config(dbtest.RoleAdmin))

	attempts := 0
	err := pool.WithTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(tx pgx.Tx) error {
		attempts++
		if _, err := tx.Exec(ctx, `INSERT INTO audit_events (id, action, outcome) VALUES (gen_random_uuid(), 'retry.test', 'success')`); err != nil {
			return err
		}
		if attempts < 3 {
			// A real server-side serialization failure, as a concurrent serializable transaction would cause.
			_, err := tx.Exec(ctx, `DO $$ BEGIN RAISE EXCEPTION 'simulated' USING ERRCODE = '40001'; END $$`)
			return err
		}
		return nil
	})
	if err != nil || attempts != 3 {
		t.Fatalf("attempts=%d err=%v; want success on the third attempt", attempts, err)
	}
	super := env.Super(ctx)
	defer super.Close(ctx)
	var n int
	if err := super.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE action = 'retry.test'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("rows=%d err=%v; the failed attempts must have rolled back, leaving exactly one row", n, err)
	}
}

func TestTransactionGivesUpAfterThreeSerializationFailures(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	env := dbtest.NewMigrated(t)
	pool := openPool(t, env.Config(dbtest.RoleAdmin))

	attempts := 0
	err := pool.WithTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		attempts++
		_, err := tx.Exec(ctx, `DO $$ BEGIN RAISE EXCEPTION 'always' USING ERRCODE = '40P01'; END $$`)
		return err
	})
	if attempts != 3 || sharederrors.CodeOf(err) != database.CodeDBSerialization || sharederrors.KindOf(err) != sharederrors.KindUnavailable {
		t.Fatalf("attempts=%d err=%v", attempts, err)
	}
}

func TestVerifyFullNeverFallsBackToPlaintext(t *testing.T) {
	t.Parallel()
	env := dbtest.NewMigrated(t)
	cfg := env.Config(dbtest.RoleApp)
	cfg.SSLMode = database.SSLVerifyFull // the test server has no TLS
	cfg.ConnectTimeout = 2 * time.Second
	pool := openPool(t, cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := pool.Check(ctx)
	if err == nil {
		t.Fatal("verify-full connected to a server without TLS: the connection silently downgraded to plaintext")
	}
	if sharederrors.KindOf(err) != sharederrors.KindUnavailable {
		t.Errorf("a failed TLS handshake must be classified unavailable, got %v: %v", sharederrors.KindOf(err), err)
	}
	if strings.Contains(err.Error(), string(cfg.Password.Reveal())) {
		t.Error("the error leaked the password")
	}
}

func TestWrongPasswordIsRefusedWithoutLeakingIt(t *testing.T) {
	t.Parallel()
	env := dbtest.NewMigrated(t)
	// Roles authenticate with a password only on loopback TCP when pg_hba requires it; on a trust-auth test server any
	// password is accepted, so this case is meaningful only where password authentication is enforced.
	cfg := env.Config(dbtest.RoleApp)
	cfg.Password = "definitely-the-wrong-password"
	pool := openPool(t, cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := pool.Check(ctx); err != nil && strings.Contains(err.Error(), "definitely-the-wrong-password") {
		t.Fatalf("the error leaked the password: %v", err)
	}
}
