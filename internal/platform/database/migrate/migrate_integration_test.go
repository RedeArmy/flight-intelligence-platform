//go:build integration

package migrate_test

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/database/dbtest"
	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/database/migrate"
	"github.com/RedeArmy/flight-intelligence-platform/migrations"
)

// latest is the highest migration version in migrations/.
const latest = 2

func tableExists(ctx context.Context, t *testing.T, env *dbtest.Env, name string) bool {
	t.Helper()
	conn := env.Super(ctx)
	defer conn.Close(ctx)
	var exists bool
	if err := conn.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, "public."+name).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	return exists
}

func TestUpAppliesEverythingFromZeroAndIsIdempotent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	env := dbtest.New(t)
	r := migrate.New(env.Config(dbtest.RoleMigrator), migrations.FS, nil)

	if v, dirty, err := r.Version(ctx); err != nil || v != 0 || dirty {
		t.Fatalf("a fresh database must report version 0: %d dirty=%v err=%v", v, dirty, err)
	}
	if err := r.Up(ctx); err != nil {
		t.Fatalf("first Up: %v", err)
	}
	if err := r.Up(ctx); err != nil {
		t.Fatalf("a second Up must be a no-op, got: %v", err)
	}
	if v, dirty, err := r.Version(ctx); err != nil || v != latest || dirty {
		t.Fatalf("version = %d dirty=%v err=%v, want %d clean", v, dirty, err, latest)
	}
	for _, table := range []string{"api_clients", "api_keys", "audit_events"} {
		if !tableExists(ctx, t, env, table) {
			t.Errorf("table %s was not created", table)
		}
	}
}

func TestDownRollsBackStepByStepAndUpRestores(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	env := dbtest.NewMigrated(t)
	r := migrate.New(env.Config(dbtest.RoleMigrator), migrations.FS, nil)

	if err := r.Down(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if v, _, _ := r.Version(ctx); v != latest-1 || tableExists(ctx, t, env, "audit_events") || !tableExists(ctx, t, env, "api_keys") {
		t.Fatalf("after one step down: version %d", v)
	}
	if err := r.Down(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if v, _, _ := r.Version(ctx); v != 0 || tableExists(ctx, t, env, "api_keys") {
		t.Fatalf("after two steps down: version %d", v)
	}
	if err := r.Up(ctx); err != nil {
		t.Fatalf("Up after a full rollback: %v", err)
	}
	if v, dirty, _ := r.Version(ctx); v != latest || dirty {
		t.Fatalf("version = %d dirty=%v", v, dirty)
	}
}

func TestMigratorOwnsTheSchemaObjects(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	env := dbtest.NewMigrated(t)
	conn := env.Super(ctx)
	defer conn.Close(ctx)

	rows, err := conn.Query(ctx, `SELECT tablename, tableowner FROM pg_tables WHERE schemaname = 'public' AND tablename IN ('api_clients','api_keys','audit_events')`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	seen := 0
	for rows.Next() {
		var table, owner string
		if err := rows.Scan(&table, &owner); err != nil {
			t.Fatal(err)
		}
		seen++
		if owner != dbtest.RoleMigrator {
			t.Errorf("%s is owned by %s, want %s: the runtime role must never own schema objects", table, owner, dbtest.RoleMigrator)
		}
	}
	if seen != 3 {
		t.Errorf("found %d application tables, want 3", seen)
	}
}

func TestConcurrentRunnersDoNotCorruptTheSchema(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	env := dbtest.New(t)

	var wg sync.WaitGroup
	errs := make(chan error, 3)
	for range 3 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- migrate.New(env.Config(dbtest.RoleMigrator), migrations.FS, nil).Up(ctx)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("concurrent Up failed: %v", err)
		}
	}
	if v, dirty, err := migrate.New(env.Config(dbtest.RoleMigrator), migrations.FS, nil).Version(ctx); err != nil || v != latest || dirty {
		t.Fatalf("version = %d dirty=%v err=%v", v, dirty, err)
	}
}

func TestTheRuntimeRoleCannotRunMigrations(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	env := dbtest.New(t) // roles exist, no schema yet
	r := migrate.New(env.Config(dbtest.RoleApp), migrations.FS, nil)

	err := r.Up(ctx)
	if err == nil {
		t.Fatal("fip_app must not be able to create the schema")
	}
	if strings.Contains(err.Error(), string(env.Password(dbtest.RoleApp).Reveal())) || strings.Contains(err.Error(), "pgx5://") {
		t.Errorf("the error leaked the connection string or password: %v", err)
	}
}
