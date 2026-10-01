package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mergeString(t *testing.T, in string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	err := merge(strings.NewReader(in), &out)
	return out.String(), err
}

func TestMergeSumsDuplicateBlocksAndKeepsOneLineEach(t *testing.T) {
	got, err := mergeString(t, strings.Join([]string{
		"mode: atomic",
		"a/x.go:1.1,3.2 2 0", // reported by two test binaries: never ran in the first
		"a/x.go:1.1,3.2 2 5",
		"a/x.go:5.1,6.2 1 0", // never ran anywhere
		"a/y.go:1.1,2.2 3 1",
		"a/y.go:1.1,2.2 3 4",
	}, "\n"))
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		"mode: atomic",
		"a/x.go:1.1,3.2 2 5",
		"a/x.go:5.1,6.2 1 0",
		"a/y.go:1.1,2.2 3 5",
	}, "\n") + "\n"
	if got != want {
		t.Fatalf("merged profile:\n%s\nwant:\n%s", got, want)
	}
}

func TestMergeIsDeterministicAndIdempotent(t *testing.T) {
	in := "mode: set\nb.go:1.1,2.2 1 1\na.go:1.1,2.2 1 0\nb.go:1.1,2.2 1 1\n"
	first, err := mergeString(t, in)
	if err != nil {
		t.Fatal(err)
	}
	second, err := mergeString(t, first)
	if err != nil || first != second {
		t.Fatalf("merging a merged profile must change nothing:\n%s\n---\n%s (err %v)", first, second, err)
	}
	if !strings.HasPrefix(first, "mode: set\na.go") {
		t.Errorf("blocks must be sorted: %q", first)
	}
}

func TestMergeRejectsBadInput(t *testing.T) {
	cases := map[string]string{
		"no mode line":            "a.go:1.1,2.2 1 1\n",
		"mixed modes":             "mode: set\nmode: atomic\n",
		"short line":              "mode: set\na.go:1.1,2.2 1\n",
		"non-numeric statements":  "mode: set\na.go:1.1,2.2 x 1\n",
		"non-numeric count":       "mode: set\na.go:1.1,2.2 1 y\n",
		"inconsistent statements": "mode: set\na.go:1.1,2.2 1 1\na.go:1.1,2.2 2 1\n",
		"empty input":             "",
	}
	for name, in := range cases {
		if _, err := mergeString(t, in); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestMergeFilesReadsAndWrites(t *testing.T) {
	dir := t.TempDir()
	in, out := filepath.Join(dir, "raw.out"), filepath.Join(dir, "merged.out")
	if err := os.WriteFile(in, []byte("mode: atomic\na.go:1.1,2.2 1 0\na.go:1.1,2.2 1 2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := mergeFiles(in, out); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(out)
	if string(got) != "mode: atomic\na.go:1.1,2.2 1 2\n" {
		t.Fatalf("output = %q", got)
	}
	if err := mergeFiles(filepath.Join(dir, "missing.out"), out); err == nil {
		t.Error("a missing input must be an error")
	}
	if err := mergeFiles(in, filepath.Join(dir, "no-such-dir", "x.out")); err == nil {
		t.Error("an unwritable output must be an error")
	}
}
