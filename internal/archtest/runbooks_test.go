package archtest

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// runbookHeadings are the headings every runbook has, in this order (docs/operations/runbooks/TEMPLATE.md).
var runbookHeadings = []string{
	"## Status", "## When to use", "## Impact", "## Before you start", "## Steps", "## Verification", "## If it goes wrong", "## Follow-up",
}

// requiredRunbooks are the runbooks E1 promises (E1-15). Removing one must fail the build, not pass silently.
var requiredRunbooks = []string{"rollback", "key-rotation", "provider-disable", "db-restore"}

var (
	stateLine    = regexp.MustCompile(`(?m)^\*\*State:\*\* (Ready|Skeleton|Blocked)\s*$`)
	verifiedLine = regexp.MustCompile(`(?m)^\*\*Last verified:\*\* (\d{4}-\d{2}-\d{2}|never)\s*$`)
)

// checkRunbook returns the problems of one runbook's text. isTemplate relaxes the status rules, because the template
// shows the allowed values instead of choosing one.
func checkRunbook(text string, isTemplate bool) []string {
	var problems []string
	if !strings.HasPrefix(text, "# Runbook: ") {
		problems = append(problems, `the first line must be a title starting with "# Runbook: "`)
	}

	lines := strings.Split(text, "\n")
	next := 0
	for _, line := range lines {
		if next < len(runbookHeadings) && strings.TrimRight(line, " \t\r") == runbookHeadings[next] {
			next++
		}
	}
	if next < len(runbookHeadings) {
		problems = append(problems, "missing or out of order heading: "+runbookHeadings[next])
	}
	if isTemplate {
		return problems
	}

	state := stateLine.FindStringSubmatch(text)
	verified := verifiedLine.FindStringSubmatch(text)
	switch {
	case state == nil:
		problems = append(problems, "the Status section needs a line `**State:** Ready`, `Skeleton` or `Blocked`")
	case verified == nil:
		problems = append(problems, "the Status section needs a line `**Last verified:** YYYY-MM-DD` or `never`")
	case state[1] == "Ready" && verified[1] == "never":
		problems = append(problems, "a Ready runbook must say when it was verified: it was never run, so it is not Ready")
	case state[1] != "Ready" && verified[1] != "never":
		problems = append(problems, "a "+state[1]+" runbook cannot have a verification date: say `never`, or mark it Ready after running it")
	}
	return problems
}

func runbookDir(t *testing.T) string {
	t.Helper()
	return filepath.Join(moduleRoot(t), "docs", "operations", "runbooks")
}

func TestRunbooksFollowTheTemplate(t *testing.T) {
	dir := runbookDir(t)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".md") || name == "README.md" {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range checkRunbook(string(b), name == "TEMPLATE.md") {
			t.Errorf("docs/operations/runbooks/%s: %s", name, p)
		}
		checked++
	}
	if checked == 0 {
		t.Fatal("no runbooks found; the test is not checking anything")
	}
}

func TestEveryPromisedRunbookExistsAndIsIndexed(t *testing.T) {
	dir := runbookDir(t)
	readme, err := os.ReadFile(filepath.Join(dir, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range requiredRunbooks {
		if _, err := os.Stat(filepath.Join(dir, name+".md")); err != nil {
			t.Errorf("the runbook %s.md is required by E1-15 and is missing", name)
		}
		if !strings.Contains(string(readme), "]("+name+".md)") {
			t.Errorf("README.md does not link %s.md", name)
		}
	}
}

func TestEveryRunbookFileIsListedInTheIndex(t *testing.T) {
	dir := runbookDir(t)
	readme, err := os.ReadFile(filepath.Join(dir, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob(filepath.Join(dir, "*.md"))
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(files)
	for _, f := range files {
		base := filepath.Base(f)
		if base == "README.md" {
			continue
		}
		if !strings.Contains(string(readme), "]("+base+")") {
			t.Errorf("%s exists but README.md does not link it", base)
		}
	}
}

func TestCheckRunbookCatchesWhatItShould(t *testing.T) {
	good := "# Runbook: do a thing\n\n## Status\n**State:** Ready\n**Last verified:** 2026-10-02\n\n## When to use\n## Impact\n## Before you start\n## Steps\n## Verification\n## If it goes wrong\n## Follow-up\n"
	if p := checkRunbook(good, false); len(p) != 0 {
		t.Fatalf("a valid runbook was refused: %v", p)
	}
	cases := map[string]struct {
		text string
		want string
	}{
		"no title":          {strings.Replace(good, "# Runbook: do a thing", "# Something", 1), "title"},
		"missing heading":   {strings.Replace(good, "## Impact\n", "", 1), "## Impact"},
		"headings reversed": {strings.Replace(strings.Replace(good, "## Steps", "## TMP", 1), "## Verification", "## Steps", 1) + "\n## Verification\n", "out of order"},
		"no state":          {strings.Replace(good, "**State:** Ready\n", "", 1), "State"},
		"unknown state":     {strings.Replace(good, "Ready", "Done", 1), "State"},
		"no verified line":  {strings.Replace(good, "**Last verified:** 2026-10-02\n", "", 1), "Last verified"},
		"bad date":          {strings.Replace(good, "2026-10-02", "yesterday", 1), "Last verified"},
		"ready but never":   {strings.Replace(good, "2026-10-02", "never", 1), "not Ready"},
		"skeleton verified": {strings.Replace(good, "Ready", "Skeleton", 1), "cannot have a verification date"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			p := checkRunbook(c.text, false)
			if len(p) == 0 || !strings.Contains(strings.Join(p, "; "), c.want) {
				t.Fatalf("problems = %v, want one mentioning %q", p, c.want)
			}
		})
	}
	skeleton := strings.Replace(strings.Replace(good, "Ready", "Skeleton", 1), "2026-10-02", "never", 1)
	if p := checkRunbook(skeleton, false); len(p) != 0 {
		t.Errorf("a skeleton that says never is valid: %v", p)
	}
	if p := checkRunbook(strings.Replace(good, "**State:** Ready\n**Last verified:** 2026-10-02\n", "**State:** Ready | Skeleton | Blocked\n", 1), true); len(p) != 0 {
		t.Errorf("the template shows the options and must pass: %v", p)
	}
}
