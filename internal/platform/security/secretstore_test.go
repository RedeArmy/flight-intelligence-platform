package security

import (
	"context"
	stderrors "errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/config"
	sharederrors "github.com/RedeArmy/flight-intelligence-platform/internal/shared/errors"
)

func lookupOf(m map[string]string) config.Lookup {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

func writeSecret(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLocalStoreRefusesProductionLikeEnvironments(t *testing.T) {
	for _, env := range []config.Env{config.EnvStaging, config.EnvProduction, config.Env(""), config.Env("prod")} {
		if _, err := NewLocalStore(env, lookupOf(nil), ""); !stderrors.Is(err, ErrLocalStoreForbidden) {
			t.Errorf("env %q: err = %v, want ErrLocalStoreForbidden", env, err)
		}
	}
	for _, env := range []config.Env{config.EnvLocal, config.EnvTest} {
		if _, err := NewLocalStore(env, lookupOf(nil), ""); err != nil {
			t.Errorf("env %q must be allowed: %v", env, err)
		}
	}
}

func TestLocalStoreReadsEnvironmentThenFile(t *testing.T) {
	dir := t.TempDir()
	writeSecret(t, dir, "db_password", "from-file\n")
	writeSecret(t, dir, "only_file", "file-value\r\n")
	store, err := NewLocalStore(config.EnvTest, lookupOf(map[string]string{"SECRET_DB_PASSWORD": "  from-env  "}), dir)
	if err != nil {
		t.Fatal(err)
	}

	got, err := store.Get(context.Background(), "db_password")
	if err != nil || got.Reveal() != "from-env" {
		t.Fatalf("environment must win and be trimmed: %q, %v", got.Reveal(), err)
	}
	got, err = store.Get(context.Background(), "only_file")
	if err != nil || got.Reveal() != "file-value" {
		t.Fatalf("file value must lose its trailing newline: %q, %v", got.Reveal(), err)
	}
}

func TestLocalStoreMissingOrEmptySecretIsNotFound(t *testing.T) {
	dir := t.TempDir()
	writeSecret(t, dir, "empty_one", "\n")
	store, _ := NewLocalStore(config.EnvLocal, lookupOf(map[string]string{"SECRET_BLANK_ENV": "   "}), dir)

	for _, name := range []string{"absent_one", "empty_one", "blank_env"} {
		_, err := store.Get(context.Background(), name)
		if !stderrors.Is(err, ErrSecretNotFound) || sharederrors.KindOf(err) != sharederrors.KindNotFound {
			t.Errorf("%s: err = %v, want not found", name, err)
		}
	}
	noDir, _ := NewLocalStore(config.EnvLocal, lookupOf(nil), "")
	if _, err := noDir.Get(context.Background(), "anything_x"); !stderrors.Is(err, ErrSecretNotFound) {
		t.Errorf("no directory configured: %v", err)
	}
	missingDir, _ := NewLocalStore(config.EnvLocal, lookupOf(nil), filepath.Join(dir, "does-not-exist"))
	if _, err := missingDir.Get(context.Background(), "anything_x"); !stderrors.Is(err, ErrSecretNotFound) {
		t.Errorf("missing directory: %v", err)
	}
}

func TestLocalStoreRejectsNamesThatCouldEscapeTheDirectory(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(filepath.Dir(dir), "outside_secret")
	if err := os.WriteFile(outside, []byte("do-not-read"), 0o600); err != nil {
		t.Fatal(err)
	}
	store, _ := NewLocalStore(config.EnvLocal, lookupOf(nil), dir)

	for _, name := range []string{"../outside_secret", "..\\outside_secret", "/etc/passwd", "a/b", "UPPER", "x", "", "has space", "dot.name", strings.Repeat("a", 65)} {
		_, err := store.Get(context.Background(), name)
		if sharederrors.KindOf(err) != sharederrors.KindInvalid || sharederrors.CodeOf(err) != CodeSecretNameInvalid {
			t.Errorf("name %q: err = %v, want invalid name", name, err)
		}
	}
}

func TestLocalStoreRefusesOversizedSecretFiles(t *testing.T) {
	dir := t.TempDir()
	writeSecret(t, dir, "huge_secret", strings.Repeat("x", maxSecretBytes+1))
	writeSecret(t, dir, "max_secret", strings.Repeat("x", maxSecretBytes))
	store, _ := NewLocalStore(config.EnvLocal, lookupOf(nil), dir)

	if _, err := store.Get(context.Background(), "huge_secret"); err == nil || stderrors.Is(err, ErrSecretNotFound) {
		t.Errorf("oversized file must be an error, got %v", err)
	}
	if v, err := store.Get(context.Background(), "max_secret"); err != nil || len(v.Reveal()) != maxSecretBytes {
		t.Errorf("a file at the limit must be accepted: %v", err)
	}
}

func TestLocalStoreHonoursCancelledContext(t *testing.T) {
	store, _ := NewLocalStore(config.EnvLocal, lookupOf(map[string]string{"SECRET_X_Y": "v"}), "")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.Get(ctx, "x_y"); !stderrors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
}

func TestSecretValuesNeverAppearInErrorsOrFormatting(t *testing.T) {
	dir := t.TempDir()
	writeSecret(t, dir, "my_secret", "super-sensitive-value")
	store, _ := NewLocalStore(config.EnvLocal, lookupOf(nil), dir)
	got, err := store.Get(context.Background(), "my_secret")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got.String(), "super-sensitive") || strings.Contains(strings.TrimSpace(strings.Join([]string{got.String()}, "")), "super") {
		t.Fatal("the returned Secret must redact itself")
	}
}
