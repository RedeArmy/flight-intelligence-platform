// Command migrationcheck fails when a migration that already exists on the base branch has been edited, renamed or
// deleted.
//
// A migration that reached the base branch may already have run in other environments, so changing it in place would
// leave those databases different from a freshly built one. A mistake is fixed with a NEW migration (expand/contract,
// ADR-015). Only added files are allowed under migrations/*.sql.
//
// Usage: go run ./scripts/migrationcheck [base-ref]     (default origin/main; compared from the merge base)
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

const (
	migrationsDir = "migrations"
	gitTimeout    = 30 * time.Second
)

func main() {
	base := "origin/main"
	if len(os.Args) > 1 {
		base = os.Args[1]
	}
	os.Exit(run(os.Stdout, execGit{}, base, "HEAD", os.Getenv("GITHUB_ACTIONS") == "true"))
}

// git runs a git command and returns its standard output.
type git interface {
	Output(args ...string) (string, error)
}

// execGit runs the real git binary in the current directory (or in dir when set, which the tests use).
type execGit struct{ dir string }

func (g execGit) Output(args ...string) (string, error) {
	// A hung git must not hang CI: every call is bounded.
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...) // #nosec G204 -- fixed subcommands; refs are validated by validRef before use
	cmd.Dir = g.dir
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

var errNoBase = errors.New("base ref not found")

// validRef rejects values git would read as an option (for example --output=...) instead of a revision.
func validRef(ref string) bool {
	return ref != "" && !strings.HasPrefix(ref, "-") && !strings.ContainsAny(ref, " \t\r\n")
}

// run prints the result and returns the process exit code.
func run(out io.Writer, g git, base, head string, ci bool) int {
	if !validRef(base) || !validRef(head) {
		fmt.Fprintf(out, "migrationcheck: %q and %q must be plain git revisions\n", base, head)
		return 2
	}
	violations, added, mergeBase, err := check(g, base, head)
	switch {
	case errors.Is(err, errNoBase):
		fmt.Fprintf(out, "Base ref %s not found; nothing to compare.\n", base)
		return 0
	case err != nil:
		fmt.Fprintf(out, "migrationcheck: %v\n", err)
		return 2
	case len(violations) > 0:
		if ci {
			fmt.Fprintln(out, "::error::Applied migrations must never be edited, renamed or deleted. Add a new migration instead.")
		} else {
			fmt.Fprintln(out, "Applied migrations must never be edited, renamed or deleted. Add a new migration instead.")
		}
		fmt.Fprintf(out, "Changed relative to %s (merge base with %s):\n", short(mergeBase), base)
		for _, v := range violations {
			fmt.Fprintln(out, "  "+v)
		}
		return 1
	}
	fmt.Fprintf(out, "OK: no existing migration was modified (%d new migration file(s) since %s).\n", added, short(mergeBase))
	return 0
}

func short(rev string) string {
	if len(rev) > 7 {
		return rev[:7]
	}
	return rev
}

// check compares head with the merge base of base and head. It returns the changes that are not plain additions, the
// number of added migration files, and the merge base.
func check(g git, base, head string) (violations []string, added int, mergeBase string, err error) {
	if _, verr := g.Output("rev-parse", "--verify", "--quiet", base+"^{commit}"); verr != nil {
		return nil, 0, "", errNoBase
	}
	mergeBase, err = g.Output("merge-base", base, head)
	if err != nil {
		return nil, 0, "", fmt.Errorf("find the merge base of %s and %s: %w", base, head, err)
	}
	// --no-renames reports a rename as a delete plus an add, so renaming an applied migration is caught as well.
	diff, err := g.Output("diff", "--name-status", "--no-renames", mergeBase, head, "--", migrationsDir)
	if err != nil {
		return nil, 0, "", fmt.Errorf("diff %s..%s: %w", short(mergeBase), head, err)
	}
	violations, added = classify(diff)
	return violations, added, mergeBase, nil
}

// classify splits `git diff --name-status` output into non-additions and a count of added .sql files.
func classify(diff string) (violations []string, added int) {
	for _, line := range strings.Split(diff, "\n") {
		status, path, ok := strings.Cut(strings.TrimSpace(line), "\t")
		if !ok || !strings.HasSuffix(path, ".sql") {
			continue // blank lines and non-migration files (README, Go) may change freely
		}
		if status == "A" {
			added++
			continue
		}
		violations = append(violations, status+"\t"+path)
	}
	return violations, added
}
