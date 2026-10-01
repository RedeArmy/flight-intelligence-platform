// Package logging builds the platform's structured logger (ADR-019, SR-09).
//
// The logger emits JSON (or text for local development), tags every record with service, environment and
// version, adds request, correlation and trace IDs from the context, and redacts credentials: sensitive keys,
// secret.Secret values, API keys, bearer tokens and URL passwords never reach the output.
package logging

import (
	"fmt"
	"io"
	"log/slog"
	"os"
)

// Options configures New.
type Options struct {
	Service string    // e.g. "api" or "worker"
	Env     string    // deployment environment
	Version string    // build or release version
	Level   string    // debug | info | warn | error (default info)
	Format  string    // json | text (default json)
	Out     io.Writer // default os.Stdout
	Trace   TraceExtractor
}

// ParseLevel converts a level name to a slog.Level.
func ParseLevel(s string) (slog.Level, error) {
	switch s {
	case "", "info":
		return slog.LevelInfo, nil
	case "debug":
		return slog.LevelDebug, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	}
	return 0, fmt.Errorf("unknown log level %q", s)
}

// New returns a logger that redacts credentials and enriches records with context.
func New(opts Options) (*slog.Logger, error) {
	level, err := ParseLevel(opts.Level)
	if err != nil {
		return nil, err
	}
	out := opts.Out
	if out == nil {
		out = os.Stdout
	}
	handlerOpts := &slog.HandlerOptions{Level: level, ReplaceAttr: redactAttr}

	var base slog.Handler
	switch opts.Format {
	case "", "json":
		base = slog.NewJSONHandler(out, handlerOpts)
	case "text":
		base = slog.NewTextHandler(out, handlerOpts)
	default:
		return nil, fmt.Errorf("unknown log format %q", opts.Format)
	}

	logger := slog.New(contextHandler{next: base, trace: opts.Trace})
	return logger.With(
		slog.String("service", opts.Service),
		slog.String("env", opts.Env),
		slog.String("version", opts.Version),
	), nil
}
