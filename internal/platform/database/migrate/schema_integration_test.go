//go:build integration

package migrate_test

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/database/dbtest"
	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/database/migrate"
	"github.com/RedeArmy/flight-intelligence-platform/migrations"
)

var update = flag.Bool("update", false, "rewrite testdata/schema.golden.txt from the current migrations")

const goldenPath = "testdata/schema.golden.txt"

// section is one part of the schema description: a title and the query that lists it.
type section struct {
	title string
	query string
}

// sections describe everything a migration can change that matters: tables and owners, columns, constraints, indexes
// and the privileges of every role, including PUBLIC. golang-migrate's own bookkeeping table is left out.
var sections = []section{
	{"tables (name, owner)", `
SELECT c.relname, pg_get_userbyid(c.relowner)
FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = 'public' AND c.relkind = 'r' AND c.relname <> 'schema_migrations'
ORDER BY 1`},
	{"columns (table, column, type, nullable, default)", `
SELECT table_name, column_name, data_type, is_nullable, coalesce(column_default, '')
FROM information_schema.columns
WHERE table_schema = 'public' AND table_name <> 'schema_migrations'
ORDER BY table_name, ordinal_position`},
	{"constraints (table, name, definition)", `
SELECT conrelid::regclass::text, conname, pg_get_constraintdef(oid)
FROM pg_constraint
WHERE connamespace = 'public'::regnamespace AND conrelid <> 0 AND conrelid::regclass::text <> 'schema_migrations'
ORDER BY 1, 2`},
	{"indexes (table, name, definition)", `
SELECT tablename, indexname, indexdef
FROM pg_indexes
WHERE schemaname = 'public' AND tablename <> 'schema_migrations'
ORDER BY 1, 2`},
	{"table privileges (table, grantee, privilege), owner excluded", `
SELECT c.relname, coalesce(r.rolname, 'PUBLIC'), acl.privilege_type
FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
CROSS JOIN LATERAL aclexplode(c.relacl) acl
LEFT JOIN pg_roles r ON r.oid = acl.grantee
WHERE n.nspname = 'public' AND c.relkind = 'r' AND c.relname <> 'schema_migrations' AND acl.grantee <> c.relowner
ORDER BY 1, 2, 3`},
	{"column privileges (table, column, grantee, privilege)", `
SELECT c.relname, a.attname, coalesce(r.rolname, 'PUBLIC'), acl.privilege_type
FROM pg_attribute a
JOIN pg_class c ON c.oid = a.attrelid
JOIN pg_namespace n ON n.oid = c.relnamespace
CROSS JOIN LATERAL aclexplode(a.attacl) acl
LEFT JOIN pg_roles r ON r.oid = acl.grantee
WHERE n.nspname = 'public' AND c.relkind = 'r' AND c.relname <> 'schema_migrations'
  AND a.attacl IS NOT NULL AND NOT a.attisdropped
ORDER BY 1, 2, 3, 4`},
}

// describe returns a deterministic text description of the application schema of the connected database.
func describe(ctx context.Context, t *testing.T, conn *pgx.Conn) string {
	t.Helper()
	var b strings.Builder
	for _, s := range sections {
		fmt.Fprintf(&b, "-- %s\n", s.title)
		rows, err := conn.Query(ctx, s.query)
		if err != nil {
			t.Fatalf("describing %q: %v", s.title, err)
		}
		n := 0
		for rows.Next() {
			vals, err := rows.Values()
			if err != nil {
				rows.Close()
				t.Fatal(err)
			}
			cells := make([]string, len(vals))
			for i, v := range vals {
				cells[i] = fmt.Sprint(v)
			}
			fmt.Fprintln(&b, strings.Join(cells, "\t"))
			n++
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		if n == 0 {
			fmt.Fprintln(&b, "(none)")
		}
		b.WriteString("\n")
	}
	return b.String()
}

func snapshot(ctx context.Context, t *testing.T, env *dbtest.Env) string {
	t.Helper()
	conn := env.Super(ctx)
	defer conn.Close(ctx)
	return describe(ctx, t, conn)
}

// lineDiff lists the lines that are only in want (-) or only in got (+), enough to see what changed.
func lineDiff(want, got string) string {
	count := func(s string) map[string]int {
		m := map[string]int{}
		for _, l := range strings.Split(s, "\n") {
			m[l]++
		}
		return m
	}
	w, g := count(want), count(got)
	var out []string
	for l, n := range w {
		if n > g[l] && strings.TrimSpace(l) != "" {
			out = append(out, "- "+l)
		}
	}
	for l, n := range g {
		if n > w[l] && strings.TrimSpace(l) != "" {
			out = append(out, "+ "+l)
		}
	}
	sort.Strings(out)
	return strings.Join(out, "\n")
}

// TestSchemaMatchesGolden makes every schema or privilege change explicit: the schema produced by the migrations must
// equal the committed snapshot, so a reviewer sees the change in the pull request. After an intended change, refresh the
// snapshot with: go test -tags integration -run TestSchemaMatchesGolden ./internal/platform/database/migrate -update
func TestSchemaMatchesGolden(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	got := snapshot(ctx, t, dbtest.NewMigrated(t))

	if *update {
		if err := os.MkdirAll(filepath.Dir(goldenPath), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(goldenPath, []byte(got), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Logf("rewrote %s", goldenPath)
		return
	}
	raw, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("missing %s (create it with -update): %v", goldenPath, err)
	}
	want := strings.ReplaceAll(string(raw), "\r\n", "\n")
	if got != want {
		t.Fatalf("the schema produced by the migrations differs from %s.\n"+
			"If the change is intended, refresh the snapshot with -update and commit it.\nDifferences:\n%s",
			goldenPath, lineDiff(want, got))
	}
}

// TestRoundTripRestoresTheSameSchemaAndLeavesNoResidue checks that rolling every migration back removes everything it
// created, and that applying them again rebuilds exactly the same schema.
func TestRoundTripRestoresTheSameSchemaAndLeavesNoResidue(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	env := dbtest.New(t)
	empty := snapshot(ctx, t, env)
	r := migrate.New(env.Config(dbtest.RoleMigrator), migrations.FS, nil)

	if err := r.Up(ctx); err != nil {
		t.Fatal(err)
	}
	first := snapshot(ctx, t, env)
	if first == empty {
		t.Fatal("applying the migrations changed nothing; the test is not checking anything")
	}

	if err := r.Down(ctx, int(latest(t))); err != nil {
		t.Fatalf("rolling everything back: %v", err)
	}
	if after := snapshot(ctx, t, env); after != empty {
		t.Fatalf("the down migrations left residue behind:\n%s", lineDiff(empty, after))
	}

	if err := r.Up(ctx); err != nil {
		t.Fatalf("re-applying: %v", err)
	}
	if again := snapshot(ctx, t, env); again != first {
		t.Fatalf("up, down and up again produced a different schema:\n%s", lineDiff(first, again))
	}
}

// TestEveryMigrationAppliesAndRevertsOnItsOwn moves one migration at a time. Each migration must apply on top of the
// previous one, be reversible at its own position, and re-apply to the same result.
func TestEveryMigrationAppliesAndRevertsOnItsOwn(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	env := dbtest.New(t)
	r := migrate.New(env.Config(dbtest.RoleMigrator), migrations.FS, nil)

	prev := snapshot(ctx, t, env)
	for v := uint(1); v <= latest(t); v++ {
		if err := r.Steps(ctx, 1); err != nil {
			t.Fatalf("applying migration %04d on top of %04d: %v", v, v-1, err)
		}
		if got, dirty, err := r.Version(ctx); err != nil || got != v || dirty {
			t.Fatalf("after applying %04d: version=%d dirty=%v err=%v", v, got, dirty, err)
		}
		applied := snapshot(ctx, t, env)

		if err := r.Steps(ctx, -1); err != nil {
			t.Fatalf("reverting migration %04d: %v", v, err)
		}
		if got := snapshot(ctx, t, env); got != prev {
			t.Fatalf("reverting %04d did not restore the previous schema:\n%s", v, lineDiff(prev, got))
		}
		if err := r.Steps(ctx, 1); err != nil {
			t.Fatalf("re-applying migration %04d: %v", v, err)
		}
		if got := snapshot(ctx, t, env); got != applied {
			t.Fatalf("re-applying %04d gave a different schema:\n%s", v, lineDiff(applied, got))
		}
		prev = applied
	}
}
