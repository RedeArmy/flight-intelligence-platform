package config

import (
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Lookup returns the value of a configuration key, as os.LookupEnv does.
type Lookup func(key string) (string, bool)

// Issue is one problem found in the configuration. Messages never contain values, so a mistakenly
// misplaced secret cannot leak through a validation error.
type Issue struct {
	Key     string
	Problem string
}

// ValidationError lists every configuration problem found, not just the first.
type ValidationError struct {
	Issues []Issue
}

func (e *ValidationError) Error() string {
	parts := make([]string, len(e.Issues))
	for i, is := range e.Issues {
		parts[i] = is.Key + ": " + is.Problem
	}
	return "invalid configuration: " + strings.Join(parts, "; ")
}

// parser reads typed values and accumulates issues. An empty default means the key is required.
type parser struct {
	lookup Lookup
	seen   map[string]struct{}
	issues []Issue
}

func newParser(lookup Lookup) *parser {
	return &parser{lookup: lookup, seen: map[string]struct{}{}}
}

func (p *parser) fail(key, problem string) { p.issues = append(p.issues, Issue{key, problem}) }

func (p *parser) err() error {
	if len(p.issues) == 0 {
		return nil
	}
	return &ValidationError{Issues: p.issues}
}

// keys returns the keys read so far, sorted.
func (p *parser) keys() []string {
	out := make([]string, 0, len(p.seen))
	for k := range p.seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// raw returns the trimmed value, treating an unset or empty variable as absent.
func (p *parser) raw(key string) (string, bool) {
	p.seen[key] = struct{}{}
	if p.lookup == nil {
		return "", false
	}
	v, ok := p.lookup(key)
	v = strings.TrimSpace(v)
	if !ok || v == "" {
		return "", false
	}
	return v, true
}

// value returns the raw value or def; it records an issue and returns false when both are empty.
func (p *parser) value(key, def string) (string, bool) {
	if v, ok := p.raw(key); ok {
		return v, true
	}
	if def == "" {
		p.fail(key, "is required")
		return "", false
	}
	return def, true
}

func (p *parser) str(key, def string) string {
	v, _ := p.value(key, def)
	return v
}

// optional returns the value, or "" when the key is not set. Unlike str it never reports a missing key.
func (p *parser) optional(key string) string {
	v, _ := p.raw(key)
	return v
}

func (p *parser) enum(key, def string, allowed ...string) string {
	v, ok := p.value(key, def)
	if !ok {
		return ""
	}
	for _, a := range allowed {
		if v == a {
			return v
		}
	}
	p.fail(key, "must be one of: "+strings.Join(allowed, ", "))
	return ""
}

func (p *parser) duration(key string, def time.Duration) time.Duration {
	v, _ := p.value(key, def.String())
	d, err := time.ParseDuration(v)
	if err != nil {
		p.fail(key, "must be a duration such as 5s or 2m")
		return 0
	}
	if d <= 0 {
		p.fail(key, "must be greater than zero")
		return 0
	}
	return d
}

func (p *parser) int64(key string, def, minValue, maxValue int64) int64 {
	v, _ := p.value(key, strconv.FormatInt(def, 10))
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		p.fail(key, "must be an integer")
		return 0
	}
	if n < minValue || n > maxValue {
		p.fail(key, fmt.Sprintf("must be between %d and %d", minValue, maxValue))
		return 0
	}
	return n
}

// optionalAddr is addr for a key that may be left unset: it returns "" when the key is not set.
func (p *parser) optionalAddr(key string) string {
	if _, ok := p.raw(key); !ok {
		return ""
	}
	return p.addr(key, "")
}

// addr validates a listen address of the form host:port (host may be empty).
func (p *parser) addr(key, def string) string {
	v, ok := p.value(key, def)
	if !ok {
		return ""
	}
	_, port, err := net.SplitHostPort(v)
	if err != nil {
		p.fail(key, "must be host:port, for example :8080 or 127.0.0.1:8081")
		return ""
	}
	if n, err := strconv.Atoi(port); err != nil || n < 0 || n > 65535 {
		p.fail(key, "port must be a number between 0 and 65535")
		return ""
	}
	return v
}
