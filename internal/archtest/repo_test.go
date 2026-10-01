package archtest

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// moduleRoot walks up from the test's working directory to the directory containing go.mod.
func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found above test directory")
		}
		dir = parent
	}
}

// listPackages returns the module's packages as reported by `go list`.
func listPackages(t *testing.T, root string) []Package {
	t.Helper()
	cmd := exec.Command("go", "list", "-e", "-json", "./...")
	cmd.Dir = root
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list failed: %v\n%s", err, stderr.String())
	}

	type listed struct {
		ImportPath   string
		Imports      []string
		TestImports  []string
		XTestImports []string
	}
	var pkgs []Package
	dec := json.NewDecoder(bytes.NewReader(out))
	for dec.More() {
		var l listed
		if err := dec.Decode(&l); err != nil {
			t.Fatalf("decode go list output: %v", err)
		}
		all := append(append(append([]string{}, l.Imports...), l.TestImports...), l.XTestImports...)
		pkgs = append(pkgs, Package{ImportPath: l.ImportPath, Imports: l.Imports, AllImports: all})
	}
	return pkgs
}

// TestRepositoryArchitecture applies the dependency rules to the real packages of this module.
func TestRepositoryArchitecture(t *testing.T) {
	pkgs := listPackages(t, moduleRoot(t))
	if len(pkgs) == 0 {
		t.Fatal("go list returned no packages; the test is not checking anything")
	}
	for _, v := range Check(pkgs) {
		t.Error(v.String())
	}
}

var mdLink = regexp.MustCompile(`\]\(([^)\s]+)\)`)

// skippedDirs are not scanned for markdown files.
var skippedDirs = map[string]bool{".git": true, "node_modules": true, "vendor": true, "bin": true, ".data": true}

// markdownFiles lists the repository's markdown files.
func markdownFiles(root string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && skippedDirs[d.Name()] {
			return filepath.SkipDir
		}
		if !d.IsDir() && strings.HasSuffix(path, ".md") {
			files = append(files, path)
		}
		return nil
	})
	return files, err
}

// checkableTarget reduces a markdown link target to a repository-relative path worth checking.
// It returns false for URLs, anchors, templates and non-document files.
func checkableTarget(raw string) (string, bool) {
	if strings.Contains(raw, "://") || strings.HasPrefix(raw, "mailto:") || strings.HasPrefix(raw, "#") {
		return "", false
	}
	target, _, _ := strings.Cut(raw, "#")
	target, _, _ = strings.Cut(target, "?")
	if target == "" || strings.ContainsAny(target, "<>{}$*") {
		return "", false
	}
	switch filepath.Ext(target) {
	case "", ".md", ".yml", ".yaml": // "" covers directory links
		return target, true
	}
	return "", false
}

// deadLinks returns "line: target" entries for relative links in file that do not resolve.
func deadLinks(file string) ([]string, error) {
	fh, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer fh.Close()

	var dead []string
	inFence := false
	line := 0
	sc := bufio.NewScanner(fh)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line++
		text := sc.Text()
		if strings.HasPrefix(strings.TrimSpace(text), "```") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		dead = append(dead, deadLinksInLine(file, line, text)...)
	}
	return dead, sc.Err()
}

func deadLinksInLine(file string, line int, text string) []string {
	var dead []string
	for _, m := range mdLink.FindAllStringSubmatch(text, -1) {
		target, ok := checkableTarget(m[1])
		if !ok {
			continue
		}
		resolved := filepath.Join(filepath.Dir(file), filepath.FromSlash(target))
		if _, err := os.Stat(resolved); err != nil {
			dead = append(dead, strconv.Itoa(line)+": "+m[1])
		}
	}
	return dead
}

// TestDocsRelativeLinks fails when a relative markdown link in the repo documentation is dead.
func TestDocsRelativeLinks(t *testing.T) {
	root := moduleRoot(t)
	files, err := markdownFiles(root)
	if err != nil {
		t.Fatalf("walking repository: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("no markdown files found; the test is not checking anything")
	}
	for _, f := range files {
		dead, err := deadLinks(f)
		if err != nil {
			t.Fatalf("reading %s: %v", f, err)
		}
		rel, _ := filepath.Rel(root, f)
		for _, d := range dead {
			t.Errorf("%s:%s (dead link)", filepath.ToSlash(rel), d)
		}
	}
}

func TestCheckableTarget(t *testing.T) {
	cases := map[string]bool{
		"docs/a.md":                true,
		"docs/a.md#section":        true,
		"../adr":                   true,
		"conf.yaml":                true,
		"https://example.com/a.md": false,
		"mailto:a@b.c":             false,
		"#anchor":                  false,
		"image.png":                false,
		"<placeholder>.md":         false,
	}
	for in, want := range cases {
		if _, got := checkableTarget(in); got != want {
			t.Errorf("checkableTarget(%q) = %v, want %v", in, got, want)
		}
	}
}
