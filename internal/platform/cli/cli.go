// Package cli holds what the operator commands (cmd/migrate, cmd/keyctl) share: signal handling, the exit code and
// loading the configuration. Each command keeps only its own arguments and behaviour.
package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/config"
)

// Options are the inputs of a command, injectable so tests do not need a real environment.
type Options struct {
	Lookup     config.Lookup
	DotEnvPath string // "" disables the .env file
	Stdout     io.Writer
}

// Runner is a command's behaviour.
type Runner func(ctx context.Context, args []string, o Options) error

// Main wires the operating system: signals, the real environment and the process exit code.
func Main(name string, run Runner) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := RealMain(ctx, name, os.Args[1:], Options{Lookup: config.LookupFromEnv(), DotEnvPath: ".env", Stdout: os.Stdout}, os.Stderr, run)
	stop()
	os.Exit(code)
}

// RealMain runs the command and turns its result into an exit code: 0 on success, 1 with the error on stderr.
func RealMain(ctx context.Context, name string, args []string, o Options, stderr io.Writer, run Runner) int {
	if err := run(ctx, args, o); err != nil {
		fmt.Fprintln(stderr, name+":", err)
		return 1
	}
	return 0
}

// LoadConfig applies the optional .env file and loads the typed configuration. It also returns the lookup that
// produced it, for components (the secret store) that read from the same source.
func LoadConfig(o Options) (config.Config, config.Lookup, error) {
	lookup, err := config.LookupWithDotEnv(o.DotEnvPath, o.Lookup)
	if err != nil {
		return config.Config{}, nil, err
	}
	cfg, err := config.Load(lookup)
	if err != nil {
		return config.Config{}, nil, err
	}
	return cfg, lookup, nil
}
