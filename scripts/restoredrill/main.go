// Command restoredrill proves that the local database can be restored from a backup (ADR-023).
//
// It dumps the database of the running Compose PostgreSQL, restores the dump into a scratch database next to it,
// compares the two (the same tables, with the same number of rows, at the same migration version), prints how long each
// step took and removes the scratch database and the dump. A restore that has never been tried is not a backup.
//
//	make restore-drill
//
// It changes nothing in the source database. The superuser password is read inside the container from the Docker
// secret, so it never appears on the host command line.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"
)

// runner executes a shell script inside the PostgreSQL container and returns its standard output.
type runner interface {
	Run(ctx context.Context, script string) (string, error)
}

type options struct {
	Source  string // database to back up
	Scratch string // database the dump is restored into
}

const dumpPath = "/tmp/restoredrill.dump"

func main() {
	composeFile := flag.String("compose-file", "deployments/local/docker-compose.yml", "Compose file of the local stack")
	service := flag.String("service", "postgres", "PostgreSQL service name")
	source := flag.String("db", "fip", "database to back up")
	scratch := flag.String("scratch", "fip_restore_drill", "scratch database for the restore")
	flag.Parse()

	r := composeRunner{file: *composeFile, service: *service}
	if err := drill(context.Background(), r, options{Source: *source, Scratch: *scratch}, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "restoredrill:", err)
		os.Exit(1)
	}
}

// composeRunner runs scripts in the container with `docker compose exec`.
type composeRunner struct {
	file, service string
}

func (c composeRunner) Run(ctx context.Context, script string) (string, error) {
	// #nosec G204 -- fixed program, arguments come from this program's flags and constants, not from external input
	cmd := exec.CommandContext(ctx, "docker", "compose", "-f", c.file, "exec", "-T", c.service, "sh", "-c", script)
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return stdout.String(), fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

// sh builds a script that exports the superuser password from the Docker secret and then runs body.
func sh(body string) string {
	return `export PGPASSWORD="$(cat /run/secrets/postgres_superuser_password)"; ` + body
}

// quoteIdent double-quotes a PostgreSQL identifier. Names come from flags, so they are checked, not trusted.
func quoteIdent(name string) (string, error) {
	if name == "" || strings.ContainsAny(name, "\"'\\;\x00 ") {
		return "", fmt.Errorf("unsafe database name %q", name)
	}
	return `"` + name + `"`, nil
}

// drill runs the whole drill and writes a report to out.
func drill(ctx context.Context, r runner, o options, out io.Writer) (err error) {
	if _, err = quoteIdent(o.Source); err != nil {
		return err
	}
	scratch, err := quoteIdent(o.Scratch)
	if err != nil {
		return err
	}
	if o.Source == o.Scratch {
		return errors.New("the scratch database must differ from the source")
	}

	total := time.Now()
	step := func(name string, fn func() error) error {
		start := time.Now()
		if err := fn(); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		fmt.Fprintf(out, "%-34s %s\n", name, time.Since(start).Round(time.Millisecond))
		return nil
	}
	// Whatever happens, remove the scratch database and the dump.
	defer func() {
		cleanup := sh(fmt.Sprintf(`psql -h localhost -U postgres -d postgres -v ON_ERROR_STOP=1 -c 'DROP DATABASE IF EXISTS %s' >/dev/null; rm -f %s`, scratch, dumpPath))
		if _, cerr := r.Run(context.WithoutCancel(ctx), cleanup); cerr != nil {
			err = errors.Join(err, fmt.Errorf("clean up: %w", cerr))
		}
	}()

	if err = step("dump "+o.Source, func() error {
		_, e := r.Run(ctx, sh(fmt.Sprintf("pg_dump -h localhost -U postgres -Fc -f %s %s", dumpPath, o.Source)))
		return e
	}); err != nil {
		return err
	}
	if err = step("create scratch database", func() error {
		_, e := r.Run(ctx, sh(fmt.Sprintf(`psql -h localhost -U postgres -d postgres -v ON_ERROR_STOP=1 -c 'DROP DATABASE IF EXISTS %s' -c 'CREATE DATABASE %s'`, scratch, scratch)))
		return e
	}); err != nil {
		return err
	}
	if err = step("restore into "+o.Scratch, func() error {
		_, e := r.Run(ctx, sh(fmt.Sprintf("pg_restore -h localhost -U postgres -d %s --no-owner --exit-on-error %s", o.Scratch, dumpPath)))
		return e
	}); err != nil {
		return err
	}

	var want, got snapshot
	if err = step("read source", func() (e error) { want, e = takeSnapshot(ctx, r, o.Source); return e }); err != nil {
		return err
	}
	if err = step("read restored copy", func() (e error) { got, e = takeSnapshot(ctx, r, o.Scratch); return e }); err != nil {
		return err
	}
	if problems := compare(want, got); len(problems) > 0 {
		return fmt.Errorf("the restored copy differs from %s:\n  - %s", o.Source, strings.Join(problems, "\n  - "))
	}
	rows := int64(0)
	for _, n := range want.Rows {
		rows += n
	}
	fmt.Fprintf(out, "\nOK: %d tables, %d rows, migration version %s restored and verified in %s\n",
		len(want.Rows), rows, want.Version, time.Since(total).Round(time.Millisecond))
	return nil
}

// snapshot is what the drill compares: the row count of every table and the migration version.
type snapshot struct {
	Rows    map[string]int64
	Version string
}

// tablesQuery lists the user tables; one row per table, schema-qualified.
const tablesQuery = `SELECT quote_ident(schemaname) || '.' || quote_ident(tablename) FROM pg_tables WHERE schemaname = 'public' ORDER BY 1`

func takeSnapshot(ctx context.Context, r runner, db string) (snapshot, error) {
	list, err := r.Run(ctx, sh(fmt.Sprintf(`psql -h localhost -U postgres -d %s -At -c "%s"`, db, tablesQuery)))
	if err != nil {
		return snapshot{}, err
	}
	tables := strings.Fields(list)
	if len(tables) == 0 {
		return snapshot{}, errors.New("the database has no tables: nothing to verify")
	}
	// One query returns "table|count" for every table, so the whole snapshot is a single consistent read.
	parts := make([]string, 0, len(tables))
	for _, t := range tables {
		parts = append(parts, fmt.Sprintf(`SELECT '%s' || '|' || count(*) FROM %s`, strings.ReplaceAll(t, "'", "''"), t))
	}
	counts, err := r.Run(ctx, sh(fmt.Sprintf(`psql -h localhost -U postgres -d %s -At -c "%s"`, db, strings.Join(parts, " UNION ALL "))))
	if err != nil {
		return snapshot{}, err
	}
	rows, err := parseCounts(counts)
	if err != nil {
		return snapshot{}, err
	}
	version, err := r.Run(ctx, sh(fmt.Sprintf(`psql -h localhost -U postgres -d %s -At -c "SELECT version || ':' || dirty FROM schema_migrations"`, db)))
	if err != nil {
		return snapshot{}, err
	}
	return snapshot{Rows: rows, Version: strings.TrimSpace(version)}, nil
}

// parseCounts reads lines of the form "table|count".
func parseCounts(s string) (map[string]int64, error) {
	rows := map[string]int64{}
	for _, line := range strings.Split(strings.TrimSpace(s), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		name, count, ok := strings.Cut(strings.TrimSpace(line), "|")
		if !ok {
			return nil, fmt.Errorf("unexpected line %q", line)
		}
		n, err := strconv.ParseInt(count, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("unexpected row count in %q", line)
		}
		rows[name] = n
	}
	return rows, nil
}

// compare returns one message per difference, sorted, or nil when the snapshots match.
func compare(want, got snapshot) []string {
	var problems []string
	names := map[string]struct{}{}
	for n := range want.Rows {
		names[n] = struct{}{}
	}
	for n := range got.Rows {
		names[n] = struct{}{}
	}
	sorted := make([]string, 0, len(names))
	for n := range names {
		sorted = append(sorted, n)
	}
	sort.Strings(sorted)
	for _, n := range sorted {
		w, inWant := want.Rows[n]
		g, inGot := got.Rows[n]
		switch {
		case !inGot:
			problems = append(problems, fmt.Sprintf("table %s is missing from the restored copy", n))
		case !inWant:
			problems = append(problems, fmt.Sprintf("table %s exists only in the restored copy", n))
		case w != g:
			problems = append(problems, fmt.Sprintf("table %s has %d rows in the source and %d after the restore", n, w, g))
		}
	}
	if want.Version != got.Version {
		problems = append(problems, fmt.Sprintf("migration version is %q in the source and %q after the restore", want.Version, got.Version))
	}
	return problems
}
