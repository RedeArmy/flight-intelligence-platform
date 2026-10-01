package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/config"
)

func TestRealMainExitCodes(t *testing.T) {
	var stderr bytes.Buffer
	ok := func(context.Context, []string, Options) error { return nil }
	if code := RealMain(context.Background(), "tool", nil, Options{}, &stderr, ok); code != 0 || stderr.Len() != 0 {
		t.Fatalf("success: code=%d stderr=%q", code, stderr.String())
	}
	fail := func(context.Context, []string, Options) error { return errors.New("boom") }
	if code := RealMain(context.Background(), "tool", nil, Options{}, &stderr, fail); code != 1 || stderr.String() != "tool: boom\n" {
		t.Fatalf("failure: code=%d stderr=%q", code, stderr.String())
	}
}

func TestLoadConfig(t *testing.T) {
	lookup := func(k string) (string, bool) {
		v, ok := map[string]string{"APP_ENV": "test"}[k]
		return v, ok
	}
	cfg, got, err := LoadConfig(Options{Lookup: lookup})
	if err != nil || cfg.App.Env != config.Env("test") || got == nil {
		t.Fatalf("cfg=%+v err=%v", cfg.App, err)
	}
	if _, _, err := LoadConfig(Options{Lookup: func(string) (string, bool) { return "", false }}); err == nil || !strings.Contains(err.Error(), "APP_ENV") {
		t.Fatalf("a missing APP_ENV must fail: %v", err)
	}
	if _, _, err := LoadConfig(Options{Lookup: lookup, DotEnvPath: t.TempDir()}); err == nil {
		t.Fatal("an unreadable .env path must fail")
	}
}
