package main

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestParseCounts(t *testing.T) {
	got, err := parseCounts("public.api_clients|3\npublic.audit_events|12\n\n")
	if err != nil || !reflect.DeepEqual(got, map[string]int64{"public.api_clients": 3, "public.audit_events": 12}) {
		t.Fatalf("got %v, %v", got, err)
	}
	for _, bad := range []string{"no separator", "public.t|many", "public.t|"} {
		if _, err := parseCounts(bad); err == nil {
			t.Errorf("%q must be an error", bad)
		}
	}
}

func TestCompare(t *testing.T) {
	base := snapshot{Rows: map[string]int64{"public.a": 1, "public.b": 2}, Version: "2:false"}
	if p := compare(base, base); p != nil {
		t.Fatalf("identical snapshots differ: %v", p)
	}
	got := compare(base, snapshot{Rows: map[string]int64{"public.a": 1, "public.b": 5, "public.c": 0}, Version: "1:false"})
	want := []string{
		"table public.b has 2 rows in the source and 5 after the restore",
		"table public.c exists only in the restored copy",
		`migration version is "2:false" in the source and "1:false" after the restore`,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got\n%v\nwant\n%v", got, want)
	}
	missing := compare(base, snapshot{Rows: map[string]int64{"public.a": 1}, Version: "2:false"})
	if len(missing) != 1 || !strings.Contains(missing[0], "public.b is missing") {
		t.Fatalf("a table lost in the restore must be reported: %v", missing)
	}
}

func TestQuoteIdent(t *testing.T) {
	if got, err := quoteIdent("fip_restore_drill"); err != nil || got != `"fip_restore_drill"` {
		t.Fatalf("got %q, %v", got, err)
	}
	for _, bad := range []string{"", "a b", `a"b`, "a;drop", "a'b", `a\b`} {
		if _, err := quoteIdent(bad); err == nil {
			t.Errorf("%q must be refused", bad)
		}
	}
}

// fakeDB answers the drill's scripts. restored is what the scratch database reports; failOn names a step to fail.
type fakeDB struct {
	source, restored string // output of the counts query for each database
	version          string
	failOn           string
	ran              []string
}

func (f *fakeDB) Run(_ context.Context, script string) (string, error) {
	f.ran = append(f.ran, script)
	if f.failOn != "" && strings.Contains(script, f.failOn) {
		return "", errors.New("boom")
	}
	switch {
	case strings.Contains(script, "pg_tables"):
		return "public.a\npublic.b\n", nil
	case strings.Contains(script, "UNION ALL"):
		if strings.Contains(script, "-d fip_restore_drill") {
			return f.restored, nil
		}
		return f.source, nil
	case strings.Contains(script, "schema_migrations"):
		return f.version + "\n", nil
	}
	return "", nil
}

func (f *fakeDB) ranCleanup() bool {
	return len(f.ran) > 0 && strings.Contains(f.ran[len(f.ran)-1], "DROP DATABASE IF EXISTS") && strings.Contains(f.ran[len(f.ran)-1], "rm -f")
}

func TestDrillSucceedsWhenTheCopyMatches(t *testing.T) {
	db := &fakeDB{source: "public.a|1\npublic.b|2\n", restored: "public.a|1\npublic.b|2\n", version: "2:false"}
	var out bytes.Buffer
	if err := drill(context.Background(), db, options{Source: "fip", Scratch: "fip_restore_drill"}, &out); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	for _, want := range []string{"dump fip", "restore into fip_restore_drill", "OK: 2 tables, 3 rows, migration version 2:false"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("report lacks %q:\n%s", want, out.String())
		}
	}
	if !db.ranCleanup() {
		t.Fatal("the scratch database and the dump must be removed at the end")
	}
	for _, s := range db.ran {
		if strings.Contains(s, "PGPASSWORD=") && !strings.Contains(s, `$(cat /run/secrets/postgres_superuser_password)`) {
			t.Errorf("the password must come from the secret file, not appear in a script: %s", s)
		}
	}
}

func TestDrillFailsWhenTheRestoredCopyDiffersAndStillCleansUp(t *testing.T) {
	db := &fakeDB{source: "public.a|1\npublic.b|2\n", restored: "public.a|1\npublic.b|1\n", version: "2:false"}
	err := drill(context.Background(), db, options{Source: "fip", Scratch: "fip_restore_drill"}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "public.b has 2 rows in the source and 1 after the restore") {
		t.Fatalf("a restore that lost rows must fail: %v", err)
	}
	if !db.ranCleanup() {
		t.Fatal("cleanup must run after a failed drill")
	}
}

func TestDrillStopsAtTheFailingStepAndStillCleansUp(t *testing.T) {
	for _, step := range []string{"pg_dump", "CREATE DATABASE", "pg_restore"} {
		t.Run(step, func(t *testing.T) {
			db := &fakeDB{failOn: step, version: "2:false"}
			err := drill(context.Background(), db, options{Source: "fip", Scratch: "fip_restore_drill"}, &bytes.Buffer{})
			if err == nil || !strings.Contains(err.Error(), "boom") {
				t.Fatalf("err = %v", err)
			}
			if db.ranCleanup() != true {
				t.Fatal("cleanup must run")
			}
		})
	}
}

func TestDrillReportsAFailedCleanupToo(t *testing.T) {
	db := &fakeDB{source: "public.a|1\n", restored: "public.a|1\n", version: "2:false", failOn: "rm -f"}
	err := drill(context.Background(), db, options{Source: "fip", Scratch: "fip_restore_drill"}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "clean up") {
		t.Fatalf("a scratch database left behind must not go unnoticed: %v", err)
	}
}

func TestDrillRefusesUnsafeOrEqualNames(t *testing.T) {
	db := &fakeDB{}
	for _, o := range []options{{Source: "fip", Scratch: "fip"}, {Source: "fip", Scratch: "x; DROP DATABASE fip"}, {Source: "", Scratch: "s"}} {
		if err := drill(context.Background(), db, o, &bytes.Buffer{}); err == nil {
			t.Errorf("%+v must be refused", o)
		}
	}
	if len(db.ran) != 0 {
		t.Fatal("nothing may run when the names are refused")
	}
}

func TestSnapshotRefusesAnEmptyDatabase(t *testing.T) {
	r := runnerFunc(func(string) (string, error) { return "", nil })
	if _, err := takeSnapshot(context.Background(), r, "fip"); err == nil || !strings.Contains(err.Error(), "no tables") {
		t.Fatalf("an empty database proves nothing: %v", err)
	}
}

type runnerFunc func(string) (string, error)

func (f runnerFunc) Run(_ context.Context, script string) (string, error) { return f(script) }
