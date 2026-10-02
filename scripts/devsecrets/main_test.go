package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fixedRandom returns predictable bytes so tests can assert on content without real randomness.
type fixedRandom struct{ next byte }

func (f *fixedRandom) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = f.next
		f.next++
	}
	return len(p), nil
}

func TestEnsureCreatesMissingSecretsWithRestrictedContent(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "secrets")
	created, kept, err := ensure(dir, []string{"a", "b"}, &fixedRandom{})
	if err != nil {
		t.Fatal(err)
	}
	if len(created) != 2 || len(kept) != 0 {
		t.Fatalf("created=%v kept=%v", created, kept)
	}
	a, _ := os.ReadFile(filepath.Join(dir, "a"))
	b, _ := os.ReadFile(filepath.Join(dir, "b"))
	if len(a) != secretBytes*2 || len(b) != secretBytes*2 {
		t.Errorf("secret lengths = %d, %d; want %d hex characters", len(a), len(b), secretBytes*2)
	}
	if bytes.Equal(a, b) {
		t.Error("each secret must get its own random value")
	}
}

func TestEnsureNeverOverwritesAnExistingSecret(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "keep")
	if err := os.WriteFile(path, []byte("original-value"), 0o600); err != nil {
		t.Fatal(err)
	}
	created, kept, err := ensure(dir, []string{"keep", "new"}, &fixedRandom{})
	if err != nil {
		t.Fatal(err)
	}
	if len(created) != 1 || created[0] != "new" || len(kept) != 1 || kept[0] != "keep" {
		t.Fatalf("created=%v kept=%v", created, kept)
	}
	if got, _ := os.ReadFile(path); string(got) != "original-value" {
		t.Fatalf("existing secret was overwritten: %q", got)
	}
}

func TestEnsureIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := ensure(dir, secretNames, &fixedRandom{}); err != nil {
		t.Fatal(err)
	}
	created, kept, err := ensure(dir, secretNames, &fixedRandom{next: 100})
	if err != nil || len(created) != 0 || len(kept) != len(secretNames) {
		t.Fatalf("second run: created=%v kept=%v err=%v", created, kept, err)
	}
}

func TestEnsureReportsRandomnessFailure(t *testing.T) {
	_, _, err := ensure(t.TempDir(), []string{"x"}, strings.NewReader("short"))
	if err == nil {
		t.Fatal("a short random source must be an error, never a weak secret")
	}
}

func TestSecretNamesMatchComposeAndInitScript(t *testing.T) {
	read := func(rel string) string {
		b, err := os.ReadFile(filepath.FromSlash(rel))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	compose := read("../../deployments/local/docker-compose.yml")
	init := read("../../deployments/local/postgres/20-role-passwords.sql")
	// Secrets that are not PostgreSQL role passwords are declared in Compose but not read by the database init script.
	notDatabase := map[string]bool{"postgres_superuser_password": true, "api_key_pepper": true, "redis_password": true, "grafana_admin_password": true}
	for _, name := range secretNames {
		if !strings.Contains(compose, "secrets/"+name) {
			t.Errorf("%s is generated but not declared in docker-compose.yml", name)
		}
		if !notDatabase[name] && !strings.Contains(init, name) {
			t.Errorf("%s is generated but never read by the init script", name)
		}
	}
}
