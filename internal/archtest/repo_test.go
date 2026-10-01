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

// TestRepositoryArchitecture applies the dependency rules to the real packages of this module.
func TestRepositoryArchitecture(t *testing.T) {
	root := moduleRoot(t)
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
	if len(pkgs) == 0 {
		t.Fatal("go list returned no packages; the test is not checking anything")
	}
	for _, v := range Check(pkgs) {
		t.Error(v.String())
	}
}

var mdLink = regexp.MustCompile(`\]\(([^)\s]+)\)`)

// TestDocsRelativeLinks fails when a relative markdown link in the repo documentation is dead.
func TestDocsRelativeLinks(t *testing.T) {
	root := moduleRoot(t)
	var files []string
	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "vendor", "bin", ".data":
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, ".md") {
			files = append(files, path)
		}
		return nil
	})
	if walkErr != nil {
		t.Fatalf("walking repository: %v", walkErr)
	}
	if len(files) == 0 {
		t.Fatal("no markdown files found; the test is not checking anything")
	}

	for _, f := range files {
		fh, err := os.Open(f)
		if err != nil {
			t.Fatal(err)
		}
		inFence := false
		sc := bufio.NewScanner(fh)
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		line := 0
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
			for _, m := range mdLink.FindAllStringSubmatch(text, -1) {
				target := m[1]
				if strings.Contains(target, "://") || strings.HasPrefix(target, "mailto:") || strings.HasPrefix(target, "#") {
					continue
				}
				target = strings.SplitN(strings.SplitN(target, "#", 2)[0], "?", 2)[0]
				if target == "" || strings.ContainsAny(target, "<>{}$*") {
					continue
				}
				if !strings.HasSuffix(target, ".md") && filepath.Ext(target) != "" && filepath.Ext(target) != ".yml" && filepath.Ext(target) != ".yaml" {
					continue
				}
				resolved := filepath.Join(filepath.Dir(f), filepath.FromSlash(target))
				if _, err := os.Stat(resolved); err != nil {
					rel, _ := filepath.Rel(root, f)
					t.Errorf("%s:%d: dead link %q", filepath.ToSlash(rel), line, m[1])
				}
			}
		}
		fh.Close()
	}
}
