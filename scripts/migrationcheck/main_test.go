package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fakeGit answers git commands from a table keyed by the first argument.
type fakeGit struct {
	verifyErr, mergeBaseErr, diffErr error
	diff                             string
}

func (f fakeGit) Output(args ...string) (string, error) {
	switch args[0] {
	case "rev-parse":
		return "", f.verifyErr
	case "merge-base":
		return "abcdef1234567", f.mergeBaseErr
	case "diff":
		return f.diff, f.diffErr
	}
	return "", errors.New("unexpected git command")
}

func TestClassify(t *testing.T) {
	cases := []struct {
		name       string
		diff       string
		violations []string
		added      int
	}{
		{"nothing changed", "", nil, 0},
		{"only additions", "A\tmigrations/0003_x.up.sql\nA\tmigrations/0003_x.down.sql", nil, 2},
		{"modified migration", "M\tmigrations/0001_a.up.sql", []string{"M\tmigrations/0001_a.up.sql"}, 0},
		{"deleted migration", "D\tmigrations/0002_b.down.sql", []string{"D\tmigrations/0002_b.down.sql"}, 0},
		{"rename is a delete plus an add", "D\tmigrations/0001_a.up.sql\nA\tmigrations/0001_renamed.up.sql", []string{"D\tmigrations/0001_a.up.sql"}, 1},
		{"non-SQL files may change", "M\tmigrations/README.md\nM\tmigrations/migrations.go\nM\tmigrations/migrations_test.go", nil, 0},
		{"mixed", "A\tmigrations/0003_x.up.sql\nM\tmigrations/0002_b.up.sql\nM\tmigrations/README.md", []string{"M\tmigrations/0002_b.up.sql"}, 1},
		{"blank lines are ignored", "\n\nA\tmigrations/0003_x.up.sql\n", nil, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v, added := classify(tc.diff)
			if strings.Join(v, "|") != strings.Join(tc.violations, "|") || added != tc.added {
				t.Fatalf("violations=%v added=%d; want %v and %d", v, added, tc.violations, tc.added)
			}
		})
	}
}

func TestRefsThatLookLikeOptionsAreRejected(t *testing.T) {
	for _, bad := range []string{"", "--output=/tmp/x", "-n1", "origin/main --foo", "a\nb"} {
		var out bytes.Buffer
		if code := run(&out, fakeGit{}, bad, "HEAD", false); code != 2 || !strings.Contains(out.String(), "plain git revisions") {
			t.Errorf("base %q: code=%d %q", bad, code, out.String())
		}
		if code := run(&out, fakeGit{}, "origin/main", bad, false); code != 2 {
			t.Errorf("head %q: code=%d", bad, code)
		}
	}
	for _, good := range []string{"origin/main", "main", "HEAD", "abc1234", "refs/heads/main", "v1.0.0"} {
		if !validRef(good) {
			t.Errorf("%q must be accepted", good)
		}
	}
}

func TestRunExitCodesAndMessages(t *testing.T) {
	cases := []struct {
		name string
		g    fakeGit
		ci   bool
		code int
		want string
	}{
		{"clean", fakeGit{diff: "A\tmigrations/0003_x.up.sql"}, false, 0, "OK: no existing migration was modified (1 new"},
		{"edited migration fails", fakeGit{diff: "M\tmigrations/0001_a.up.sql"}, false, 1, "never be edited"},
		{"CI annotation", fakeGit{diff: "M\tmigrations/0001_a.up.sql"}, true, 1, "::error::"},
		{"no CI annotation locally", fakeGit{diff: "M\tmigrations/0001_a.up.sql"}, false, 1, "Applied migrations must never"},
		{"missing base is skipped", fakeGit{verifyErr: errors.New("exit status 1")}, false, 0, "nothing to compare"},
		{"git failure is an error, not a pass", fakeGit{mergeBaseErr: errors.New("boom")}, false, 2, "merge base"},
		{"diff failure is an error, not a pass", fakeGit{diffErr: errors.New("boom")}, false, 2, "diff"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			if code := run(&out, tc.g, "origin/main", "HEAD", tc.ci); code != tc.code {
				t.Fatalf("exit code = %d, want %d\n%s", code, tc.code, out.String())
			}
			if !strings.Contains(out.String(), tc.want) {
				t.Fatalf("output %q does not contain %q", out.String(), tc.want)
			}
			if tc.name == "no CI annotation locally" && strings.Contains(out.String(), "::error::") {
				t.Error("the GitHub annotation must only be printed in CI")
			}
		})
	}
}

// repo is a throw-away git repository, so the check is also proven against real git behaviour (renames, merge base).
type repo struct {
	t   *testing.T
	dir string
}

func newRepo(t *testing.T) *repo {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	r := &repo{t: t, dir: t.TempDir()}
	r.git("init", "-q", "-b", "main")
	r.git("config", "user.email", "test@example.com")
	r.git("config", "user.name", "Test")
	r.git("config", "core.hooksPath", os.DevNull)
	r.git("config", "commit.gpgsign", "false")
	return r
}

func (r *repo) git(args ...string) string {
	r.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = r.dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func (r *repo) write(rel, content string) {
	r.t.Helper()
	full := filepath.Join(r.dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
		r.t.Fatal(err)
	}
}

func (r *repo) commit(msg string) {
	r.t.Helper()
	r.git("add", "-A")
	r.git("commit", "-q", "-m", msg)
}

// result runs the real check on the repository and returns the exit code and output.
func (r *repo) result() (int, string) {
	var out bytes.Buffer
	code := run(&out, execGit{dir: r.dir}, "main", "HEAD", false)
	return code, out.String()
}

func TestWithARealRepository(t *testing.T) {
	r := newRepo(t)
	r.write("migrations/0001_a.up.sql", "CREATE TABLE a (id int);\n")
	r.write("migrations/0001_a.down.sql", "DROP TABLE a;\n")
	r.write("migrations/README.md", "docs\n")
	r.commit("base")
	r.git("switch", "-q", "-c", "feature")

	t.Run("adding a migration and editing docs is allowed", func(t *testing.T) {
		r.write("migrations/0002_b.up.sql", "CREATE TABLE b (id int);\n")
		r.write("migrations/0002_b.down.sql", "DROP TABLE b;\n")
		r.write("migrations/README.md", "more docs\n")
		r.commit("add 0002")
		if code, out := r.result(); code != 0 || !strings.Contains(out, "2 new migration") {
			t.Fatalf("code=%d\n%s", code, out)
		}
	})

	t.Run("editing an applied migration fails", func(t *testing.T) {
		r.write("migrations/0001_a.up.sql", "CREATE TABLE a (id bigint);\n")
		r.commit("edit 0001")
		code, out := r.result()
		if code != 1 || !strings.Contains(out, "migrations/0001_a.up.sql") {
			t.Fatalf("code=%d\n%s", code, out)
		}
		r.git("revert", "--no-edit", "HEAD")
	})

	t.Run("renaming an applied migration fails", func(t *testing.T) {
		r.git("mv", "migrations/0001_a.down.sql", "migrations/0001_renamed.down.sql")
		r.commit("rename 0001 down")
		code, out := r.result()
		if code != 1 || !strings.Contains(out, "0001_a.down.sql") {
			t.Fatalf("code=%d\n%s", code, out)
		}
		r.git("revert", "--no-edit", "HEAD")
	})

	t.Run("deleting an applied migration fails", func(t *testing.T) {
		r.git("rm", "-q", "migrations/0001_a.down.sql")
		r.commit("delete 0001 down")
		code, out := r.result()
		if code != 1 || !strings.Contains(out, "D\tmigrations/0001_a.down.sql") {
			t.Fatalf("code=%d\n%s", code, out)
		}
		r.git("revert", "--no-edit", "HEAD")
	})

	t.Run("after reverting the edits the branch is clean again", func(t *testing.T) {
		if code, out := r.result(); code != 0 {
			t.Fatalf("code=%d\n%s", code, out)
		}
	})
}

func TestChangesOnTheBaseAfterBranchingAreNotFalsePositives(t *testing.T) {
	r := newRepo(t)
	r.write("migrations/0001_a.up.sql", "CREATE TABLE a (id int);\n")
	r.commit("base")
	r.git("switch", "-q", "-c", "feature")
	r.write("migrations/0003_c.up.sql", "CREATE TABLE c (id int);\n")
	r.commit("feature adds 0003")

	// main moves on and adds 0002 after the branch point; the feature branch does not have it.
	r.git("switch", "-q", "main")
	r.write("migrations/0002_b.up.sql", "CREATE TABLE b (id int);\n")
	r.commit("main adds 0002")
	r.git("switch", "-q", "feature")

	if code, out := r.result(); code != 0 {
		t.Fatalf("a file that only the base added after branching must not count as a deletion: code=%d\n%s", code, out)
	}
}

func TestUnknownBaseRefIsSkipped(t *testing.T) {
	r := newRepo(t)
	r.write("migrations/0001_a.up.sql", "CREATE TABLE a (id int);\n")
	r.commit("base")
	var out bytes.Buffer
	if code := run(&out, execGit{dir: r.dir}, "origin/main", "HEAD", false); code != 0 || !strings.Contains(out.String(), "nothing to compare") {
		t.Fatalf("code=%d\n%s", code, out.String())
	}
}
