package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func noEnv(string) (string, bool) { return "", false }

func writeEnvFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestParseDotEnv(t *testing.T) {
	in := strings.Join([]string{
		"# comment",
		"",
		"APP_ENV=local",
		"export LOG_LEVEL=debug",
		`HTTP_ADDR=":9000"`,
		"APP_VERSION='1.0 beta'",
		"  SPACED  =  value  ",
		"EMPTY=",
		"LAST=a=b=c",
	}, "\n")
	got, err := ParseDotEnv(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"APP_ENV": "local", "LOG_LEVEL": "debug", "HTTP_ADDR": ":9000", "APP_VERSION": "1.0 beta",
		"SPACED": "value", "EMPTY": "", "LAST": "a=b=c",
	}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
}

func TestParseDotEnvErrorsReportLineNumberNotContent(t *testing.T) {
	const secretLine = "this line holds a password hunter2"
	_, err := ParseDotEnv(strings.NewReader("OK=1\n" + secretLine + "\n"))
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "line 2") || strings.Contains(err.Error(), "hunter2") {
		t.Fatalf("error should name the line only: %v", err)
	}
	for _, bad := range []string{"lower=1", "1ABC=2", "=3", "NOEQUALS"} {
		if _, err := ParseDotEnv(strings.NewReader(bad)); err == nil {
			t.Errorf("%q should be rejected", bad)
		}
	}
}

func TestDotEnvFillsGapsButRealEnvironmentWins(t *testing.T) {
	path := writeEnvFile(t, "APP_ENV=local\nLOG_LEVEL=debug\nHTTP_ADDR=:7000\n")
	base := mapLookup(map[string]string{"HTTP_ADDR": ":9000"})
	look, err := LookupWithDotEnv(path, base)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(look)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Log.Level != "debug" {
		t.Errorf("file value not used: %q", cfg.Log.Level)
	}
	if cfg.HTTP.Addr != ":9000" {
		t.Errorf("real environment must win over the file, got %q", cfg.HTTP.Addr)
	}
}

func TestDotEnvIgnoredInProductionLikeEnvironments(t *testing.T) {
	path := writeEnvFile(t, "LOG_LEVEL=debug\n")
	for _, env := range []string{"production", "staging"} {
		look, err := LookupWithDotEnv(path, mapLookup(map[string]string{"APP_ENV": env}))
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := look("LOG_LEVEL"); ok {
			t.Errorf("%s: .env must be ignored", env)
		}
	}
}

func TestDotEnvCannotSelectProductionLikeEnvironment(t *testing.T) {
	for _, env := range []string{"production", "staging"} {
		path := writeEnvFile(t, "APP_ENV="+env+"\n")
		if _, err := LookupWithDotEnv(path, noEnv); err == nil {
			t.Errorf(".env selecting %s must be rejected", env)
		}
	}
}

func TestDotEnvMissingFileOrEmptyPathIsNotAnError(t *testing.T) {
	for _, path := range []string{"", filepath.Join(t.TempDir(), "absent.env"), filepath.Join(t.TempDir(), "no-dir", ".env")} {
		look, err := LookupWithDotEnv(path, mapLookup(map[string]string{"APP_ENV": "local"}))
		if err != nil {
			t.Fatalf("path %q: %v", path, err)
		}
		if v, _ := look("APP_ENV"); v != "local" {
			t.Errorf("path %q: base lookup lost", path)
		}
	}
}

func TestDotEnvSyntaxErrorIsReported(t *testing.T) {
	path := writeEnvFile(t, "APP_ENV=local\nnot valid\n")
	if _, err := LookupWithDotEnv(path, noEnv); err == nil || !strings.Contains(err.Error(), "line 2") {
		t.Fatalf("want a line-2 error, got %v", err)
	}
}
