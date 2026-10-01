package config

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const maxDotEnvBytes = 1 << 20

var dotEnvKey = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)

// ParseDotEnv parses KEY=VALUE lines. Blank lines and lines starting with # are ignored, an optional
// "export " prefix is accepted, and a value wrapped in matching single or double quotes is unwrapped.
// There is no interpolation. Errors report line numbers only, never the line content.
func ParseDotEnv(r io.Reader) (map[string]string, error) {
	out := map[string]string{}
	sc := bufio.NewScanner(io.LimitReader(r, maxDotEnvBytes))
	for n := 1; sc.Scan(); n++ {
		key, val, ok, err := parseDotEnvLine(sc.Text())
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", n, err)
		}
		if ok {
			out[key] = val
		}
	}
	return out, sc.Err()
}

var errBadDotEnvLine = errors.New("not a KEY=VALUE entry")

func parseDotEnvLine(line string) (key, val string, ok bool, err error) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return "", "", false, nil
	}
	line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
	key, val, found := strings.Cut(line, "=")
	key = strings.TrimSpace(key)
	if !found || !dotEnvKey.MatchString(key) {
		return "", "", false, errBadDotEnvLine
	}
	return key, unquote(strings.TrimSpace(val)), true, nil
}

func unquote(v string) string {
	if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
		return v[1 : len(v)-1]
	}
	return v
}

// LookupWithDotEnv returns a Lookup that falls back to the .env file at path for keys missing from base.
// Real environment variables always win. The file is read only when APP_ENV is unset or local/test, and
// it may not select a production-like environment (SR-19, ADR-018). A missing file is not an error.
func LookupWithDotEnv(path string, base Lookup) (Lookup, error) {
	if path == "" {
		return base, nil
	}
	if env, ok := base("APP_ENV"); ok && !Env(strings.TrimSpace(env)).AllowsLocalFeatures() {
		return base, nil
	}
	values, err := readDotEnvFile(path)
	if err != nil {
		return nil, err
	}
	if len(values) == 0 {
		return base, nil
	}
	if _, set := base("APP_ENV"); !set && Env(values["APP_ENV"]).IsProductionLike() {
		return nil, errors.New("a .env file may configure only local and test environments")
	}
	return func(key string) (string, bool) {
		if v, ok := base(key); ok {
			return v, true
		}
		v, ok := values[key]
		return v, ok
	}, nil
}

// readDotEnvFile reads path inside its own directory (os.OpenRoot prevents path traversal).
func readDotEnvFile(path string) (map[string]string, error) {
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("open .env directory: %w", err)
	}
	defer root.Close()

	f, err := root.Open(filepath.Base(path))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("open .env: %w", err)
	}
	defer f.Close()

	values, err := ParseDotEnv(f)
	if err != nil {
		return nil, fmt.Errorf("parse .env: %w", err)
	}
	return values, nil
}
