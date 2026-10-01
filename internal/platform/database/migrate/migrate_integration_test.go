//go:build integration

package migrate_test

import (
	"bytes"
	"context"
	"io/fs"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/database/dbtest"
	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/database/migrate"
	"github.com/RedeArmy/flight-intelligence-platform/migrations"
)

// latest returns the highest migration version in migrations/, derived from the embedded files so that adding a
// migration never requires editing a test.
func latest(t *testing.T) uint {
	t.Helper()
	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		t.Fatal(err)
	}
	var n uint
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".up.sql") {
			n++
		}
	}
	if n == 0 {
		t.Fatal("no migrations found; the test is not checking anything")
	}
	return n
}

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
	want := latest(t)

	if v, dirty, err := r.Version(ctx); err != nil || v != 0 || dirty {
		t.Fatalf("a fresh database must report version 0: %d dirty=%v err=%v", v, dirty, err)
	}
	if err := r.Up(ctx); err != nil {
		t.Fatalf("first Up: %v", err)
	}
	if err := r.Up(ctx); err != nil {
		t.Fatalf("a second Up must be a no-op, got: %v", err)
	}
	if v, dirty, err := r.Version(ctx); err != nil || v != want || dirty {
		t.Fatalf("version = %d dirty=%v err=%v, want %d clean", v, dirty, err, want)
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
	want := latest(t)

	if err := r.Down(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if v, dirty, _ := r.Version(ctx); v != want-1 || dirty {
		t.Fatalf("after one step down: version %d dirty=%v, want %d", v, dirty, want-1)
	}
	if err := r.Down(ctx, int(want-1)); err != nil {
		t.Fatal(err)
	}
	if v, dirty, _ := r.Version(ctx); v != 0 || dirty || tableExists(ctx, t, env, "api_keys") {
		t.Fatalf("after rolling everything back: version %d dirty=%v", v, dirty)
	}
	if err := r.Up(ctx); err != nil {
		t.Fatalf("Up after a full rollback: %v", err)
	}
	if v, dirty, _ := r.Version(ctx); v != want || dirty {
		t.Fatalf("version = %d dirty=%v, want %d", v, dirty, want)
	}
}

func TestMigratorOwnsTheSchemaObjects(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	env := dbtest.NewMigrated(t)
	conn := env.Super(ctx)
	defer conn.Close(ctx)

	rows, err := conn.Query(ctx, `SELECT tablename, tableowner FROM pg_tables WHERE schemaname = 'public' AND tablename <> 'schema_migrations'`)
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
	if seen == 0 {
		t.Error("found no application tables; the test is not checking anything")
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
	if v, dirty, err := migrate.New(env.Config(dbtest.RoleMigrator), migrations.FS, nil).Version(ctx); err != nil || v != latest(t) || dirty {
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

func TestProgressIsLoggedWithoutLeakingCredentials(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	env := dbtest.New(t)
	var out bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&out, nil))

	if err := migrate.New(env.Config(dbtest.RoleMigrator), migrations.FS, logger).Up(ctx); err != nil {
		t.Fatal(err)
	}
	logged := out.String()
	for _, want := range []string{"api_clients_keys", "audit_events", "component=migrate"} {
		if !strings.Contains(logged, want) {
			t.Errorf("the progress log is missing %q:\n%s", want, logged)
		}
	}
	if strings.Contains(logged, string(env.Password(dbtest.RoleMigrator).Reveal())) || strings.Contains(logged, "pgx5://") {
		t.Errorf("the progress log leaked the connection string or password:\n%s", logged)
	}
}

func TestRollingBackWhenNothingIsAppliedIsAnErrorWithoutLeakingCredentials(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	env := dbtest.New(t) // roles exist, no migrations applied
	r := migrate.New(env.Config(dbtest.RoleMigrator), migrations.FS, nil)

	err := r.Down(ctx, 1)
	if err == nil {
		t.Fatal("there is nothing to roll back; the runner must say so instead of reporting success")
	}
	if strings.Contains(err.Error(), "pgx5://") || strings.Contains(err.Error(), string(env.Password(dbtest.RoleMigrator).Reveal())) {
		t.Errorf("the error leaked the connection string or password: %v", err)
	}
}

func TestACancelledContextStopsAMigrationRun(t *testing.T) {
	t.Parallel()
	env := dbtest.New(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := migrate.New(env.Config(dbtest.RoleMigrator), migrations.FS, nil).Up(ctx); err == nil {
		t.Fatal("a cancelled context must stop the run before it changes the database")
	}
	if tableExists(context.Background(), t, env, "api_clients") {
		t.Error("nothing may be applied when the context is already cancelled")
	}
}
