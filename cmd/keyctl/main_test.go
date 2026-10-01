package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/config"
)

func lookupOf(m map[string]string) config.Lookup {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

func TestParseArgs(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		want    command
		wantErr string
	}{
		{"client create", []string{"client", "create", "-name", "a", "-role", "ADMIN"}, command{name: cmdClientCreate, client: "a", role: "ADMIN"}, ""},
		{"key issue default ttl", []string{"key", "issue", "-client", "a"}, command{name: cmdKeyIssue, client: "a", ttl: defaultKeyTTL}, ""},
		{"key issue no expiry", []string{"key", "issue", "-client", "a", "-ttl", "0"}, command{name: cmdKeyIssue, client: "a"}, ""},
		{"key issue ttl", []string{"key", "issue", "-client", "a", "-ttl", "48h"}, command{name: cmdKeyIssue, client: "a", ttl: 48 * time.Hour}, ""},
		{"key list", []string{"key", "list"}, command{name: cmdKeyList}, ""},
		{"key revoke", []string{"key", "revoke", "-prefix", "Ab3dE6gH"}, command{name: cmdKeyRevoke, prefix: "Ab3dE6gH"}, ""},
		{"no args", nil, command{}, "usage"},
		{"one arg", []string{"key"}, command{}, "usage"},
		{"unknown command", []string{"key", "burn"}, command{}, "usage"},
		{"unknown flag", []string{"key", "list", "-x"}, command{}, "usage"},
		{"stray argument", []string{"key", "list", "extra"}, command{}, "usage"},
		{"bad ttl", []string{"key", "issue", "-client", "a", "-ttl", "soon"}, command{}, "usage"},
		{"client needs name and role", []string{"client", "create", "-name", "a"}, command{}, "needs -name and -role"},
		{"issue needs client", []string{"key", "issue"}, command{}, "needs -client"},
		{"revoke needs prefix", []string{"key", "revoke"}, command{}, "needs -prefix"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := parseArgs(c.args)
			if c.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), c.wantErr) {
					t.Fatalf("err = %v, want it to contain %q", err, c.wantErr)
				}
				return
			}
			if err != nil || got != c.want {
				t.Fatalf("got %+v, %v; want %+v", got, err, c.want)
			}
		})
	}
}

func TestRealMainReportsErrorsOnStderr(t *testing.T) {
	var stderr bytes.Buffer
	code := realMain(context.Background(), []string{"nope"}, runOptions{Lookup: lookupOf(nil), Stdout: io.Discard}, &stderr)
	if code != 1 || !strings.HasPrefix(stderr.String(), "keyctl: usage") {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
}

func TestRunNeedsBothSecrets(t *testing.T) {
	dir := t.TempDir()
	env := map[string]string{"APP_ENV": "test", "SECRETS_DIR": dir}
	args := []string{"key", "list"}

	err := run(context.Background(), args, runOptions{Lookup: lookupOf(env), Stdout: io.Discard})
	if err == nil || !strings.Contains(err.Error(), "database password") {
		t.Fatalf("without the admin password: %v", err)
	}

	if err := os.WriteFile(filepath.Join(dir, adminPasswordSecret), []byte("generated"), 0o600); err != nil {
		t.Fatal(err)
	}
	err = run(context.Background(), args, runOptions{Lookup: lookupOf(env), Stdout: io.Discard})
	if err == nil || !strings.Contains(err.Error(), "pepper") {
		t.Fatalf("without the pepper: %v", err)
	}

	if err := os.WriteFile(filepath.Join(dir, pepperSecret), []byte("too-short"), 0o600); err != nil {
		t.Fatal(err)
	}
	err = run(context.Background(), args, runOptions{Lookup: lookupOf(env), Stdout: io.Discard})
	if err == nil || !strings.Contains(err.Error(), "at least") {
		t.Fatalf("a short pepper must be refused: %v", err)
	}
}

func TestRunRefusesProductionBecauseNoManagedSecretStoreExistsYet(t *testing.T) {
	env := map[string]string{"APP_ENV": "production", "POSTGRES_SSLMODE": "verify-full"}
	err := run(context.Background(), []string{"key", "list"}, runOptions{Lookup: lookupOf(env), Stdout: io.Discard})
	if err == nil || !strings.Contains(err.Error(), "production-like") {
		t.Fatalf("err = %v", err)
	}
}
