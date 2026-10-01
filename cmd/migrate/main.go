// Command migrate applies the embedded database migrations as the schema-owning role (ADR-003, ADR-031).
//
//	migrate up              apply every pending migration
//	migrate version         print the current version
//	migrate down -yes N     roll back N migrations (local development only)
//
// It reads the same configuration as the API. The password of the migrator role comes from the secret store under
// the name postgres_migrator_password; it is never a configuration value.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/config"
	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/database"
	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/database/migrate"
	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/observability/logging"
	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/security"
	"github.com/RedeArmy/flight-intelligence-platform/migrations"
)

// migratorPasswordSecret is the name of the secret in the secret store, not a credential.
const migratorPasswordSecret = "postgres_migrator_password" // #nosec G101 -- a secret name, not a secret

func main() {
	os.Exit(realMain())
}

func realMain() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	err := run(ctx, os.Args[1:], runOptions{Lookup: config.LookupFromEnv(), DotEnvPath: ".env", Stdout: os.Stdout})
	if err != nil {
		fmt.Fprintln(os.Stderr, "migrate:", err)
		return 1
	}
	return 0
}

// runOptions are the inputs of run, injectable for tests.
type runOptions struct {
	Lookup     config.Lookup
	DotEnvPath string
	Stdout     io.Writer
}

// command is a parsed command line.
type command struct {
	name      string // up | version | down
	steps     int    // down only
	confirmed bool   // down only: -yes was given
}

var errUsage = errors.New("usage: migrate up | version | down -yes <steps>")

// parseArgs parses the command line.
func parseArgs(args []string) (command, error) {
	if len(args) == 0 {
		return command{}, errUsage
	}
	cmd := command{name: args[0]}
	switch cmd.name {
	case "up", "version":
		if len(args) != 1 {
			return command{}, errUsage
		}
		return cmd, nil
	case "down":
		return parseDown(cmd, args[1:])
	}
	return command{}, errUsage
}

func parseDown(cmd command, args []string) (command, error) {
	fs := flag.NewFlagSet("down", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.BoolVar(&cmd.confirmed, "yes", false, "confirm that data will be lost")
	if err := fs.Parse(args); err != nil || fs.NArg() != 1 {
		return command{}, errUsage
	}
	steps, err := strconv.Atoi(fs.Arg(0))
	if err != nil || steps < 1 {
		return command{}, errors.New("migrate: down needs a positive number of steps")
	}
	if !cmd.confirmed {
		return command{}, errors.New("migrate: down destroys data and needs -yes (local development only)")
	}
	cmd.steps = steps
	return cmd, nil
}

func run(ctx context.Context, args []string, o runOptions) error {
	cmd, err := parseArgs(args)
	if err != nil {
		return err
	}
	lookup, err := config.LookupWithDotEnv(o.DotEnvPath, o.Lookup)
	if err != nil {
		return err
	}
	cfg, err := config.Load(lookup)
	if err != nil {
		return err
	}
	if cmd.name == "down" && !cfg.App.Env.AllowsLocalFeatures() {
		return errors.New("migrate: down is refused outside local and test environments; roll forward instead")
	}

	store, err := security.NewLocalStore(cfg.App.Env, lookup, cfg.Secrets.Dir)
	if err != nil {
		return err
	}
	password, err := store.Get(ctx, migratorPasswordSecret)
	if err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	logger, err := logging.New(logging.Options{
		Service: "migrate", Env: string(cfg.App.Env), Version: cfg.App.Version,
		Level: cfg.Log.Level, Format: cfg.Log.Format, Out: o.Stdout,
	})
	if err != nil {
		return err
	}

	dbCfg := database.FromConfig(cfg.Postgres, cfg.Postgres.MigratorUser, password, "migrate")
	runner := migrate.New(dbCfg, migrations.FS, logger)
	return execute(ctx, runner, cmd, o.Stdout)
}

func execute(ctx context.Context, r *migrate.Runner, cmd command, out io.Writer) error {
	switch cmd.name {
	case "up":
		return r.Up(ctx)
	case "down":
		return r.Down(ctx, cmd.steps)
	default: // version
		v, dirty, err := r.Version(ctx)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(out, "version=%d dirty=%v\n", v, dirty)
		return err
	}
}
