// Command keyctl manages API clients and keys as the operator role (ADR-016, ADR-027).
//
//	keyctl client create -name NAME -role ROLE    register a client (USER|DEVELOPER|OPERATOR|ADMIN|SERVICE)
//	keyctl key issue -client NAME [-ttl 2160h]    issue a key; the token is printed once and cannot be recovered
//	keyctl key list                               list keys (never any secret material)
//	keyctl key revoke -prefix PREFIX              revoke a key
//
// It connects as fip_admin. Its password and the key pepper come from the secret store (postgres_admin_password,
// api_key_pepper); they are never configuration values. Every change writes an audit event in the same transaction.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/apiauth"
	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/cli"
	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/database"
	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/httpserver"
	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/security"
	"github.com/RedeArmy/flight-intelligence-platform/internal/shared/clock"
)

// Secret names in the secret store, not credentials.
const (
	adminPasswordSecret = "postgres_admin_password" // #nosec G101 -- a secret name, not a secret
	pepperSecret        = "api_key_pepper"          // #nosec G101 -- a secret name, not a secret
)

func main() { cli.Main("keyctl", run) }

// runOptions are the inputs of run, injectable for tests.
type runOptions = cli.Options

// realMain runs the command and returns its exit code.
func realMain(ctx context.Context, args []string, o runOptions, stderr io.Writer) int {
	return cli.RealMain(ctx, "keyctl", args, o, stderr, run)
}

// command is a parsed command line.
type command struct {
	name   string // client-create | key-issue | key-list | key-revoke
	client string
	role   string
	prefix string
	ttl    time.Duration
}

var errUsage = errors.New("usage: keyctl client create -name N -role R | key issue -client N [-ttl D] | key list | key revoke -prefix P")

// defaultKeyTTL is the lifetime of a key when -ttl is not given: 90 days. Use -ttl 0 for a key that never expires.
const defaultKeyTTL = 90 * 24 * time.Hour

func parseArgs(args []string) (command, error) {
	if len(args) < 2 {
		return command{}, errUsage
	}
	cmd := command{name: args[0] + "-" + args[1]}
	fs := flag.NewFlagSet(cmd.name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	switch cmd.name {
	case "client-create":
		fs.StringVar(&cmd.client, "name", "", "client name")
		fs.StringVar(&cmd.role, "role", "", "client role")
	case "key-issue":
		fs.StringVar(&cmd.client, "client", "", "client name")
		fs.DurationVar(&cmd.ttl, "ttl", defaultKeyTTL, "key lifetime; 0 for no expiry")
	case "key-list":
	case "key-revoke":
		fs.StringVar(&cmd.prefix, "prefix", "", "key prefix")
	default:
		return command{}, errUsage
	}
	if err := fs.Parse(args[2:]); err != nil || fs.NArg() != 0 {
		return command{}, errUsage
	}
	return cmd, validate(cmd)
}

func validate(cmd command) error {
	switch {
	case cmd.name == "client-create" && (cmd.client == "" || cmd.role == ""):
		return errors.New("client create needs -name and -role")
	case cmd.name == "key-issue" && cmd.client == "":
		return errors.New("key issue needs -client")
	case cmd.name == "key-revoke" && cmd.prefix == "":
		return errors.New("key revoke needs -prefix")
	}
	return nil
}

func run(ctx context.Context, args []string, o runOptions) error {
	cmd, err := parseArgs(args)
	if err != nil {
		return err
	}
	cfg, lookup, err := cli.LoadConfig(o)
	if err != nil {
		return err
	}
	store, err := security.NewLocalStore(cfg.App.Env, lookup, cfg.Secrets.Dir)
	if err != nil {
		return err
	}
	password, err := store.Get(ctx, adminPasswordSecret)
	if err != nil {
		return fmt.Errorf("database password: %w", err)
	}
	pepper, err := store.Get(ctx, pepperSecret)
	if err != nil {
		return fmt.Errorf("API key pepper: %w", err)
	}
	hasher, err := security.NewKeyHasher(pepper)
	if err != nil {
		return err
	}
	pool, err := database.Open(ctx, database.FromConfig(cfg.Postgres, cfg.Postgres.AdminUser, password, "keyctl"))
	if err != nil {
		return err
	}
	defer pool.Close()
	return execute(ctx, apiauth.NewAdmin(pool, hasher, clock.System{}), cmd, o.Stdout)
}

func execute(ctx context.Context, admin *apiauth.Admin, cmd command, out io.Writer) error {
	switch cmd.name {
	case "client-create":
		c, err := admin.CreateClient(ctx, cmd.client, httpserver.Role(cmd.role))
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(out, "client created id=%s name=%s role=%s\n", c.ID, c.Name, c.Role)
		return err
	case "key-issue":
		return issue(ctx, admin, cmd, out)
	case "key-revoke":
		if err := admin.RevokeKey(ctx, cmd.prefix); err != nil {
			return err
		}
		_, err := fmt.Fprintf(out, "key revoked prefix=%s\n", cmd.prefix)
		return err
	default: // key-list
		return list(ctx, admin, out)
	}
}

func issue(ctx context.Context, admin *apiauth.Admin, cmd command, out io.Writer) error {
	k, err := admin.IssueKey(ctx, cmd.client, cmd.ttl)
	if err != nil {
		return err
	}
	expires := "never"
	if k.ExpiresAt != nil {
		expires = k.ExpiresAt.Format(time.RFC3339)
	}
	// The one place the full token is printed: it is shown once and is not stored anywhere (ADR-027).
	_, err = fmt.Fprintf(out, "key issued prefix=%s expires=%s\ntoken=%s\nStore the token now: it cannot be shown again.\n", k.Prefix, expires, k.Token.Reveal())
	return err
}

func list(ctx context.Context, admin *apiauth.Admin, out io.Writer) error {
	keys, err := admin.ListKeys(ctx)
	if err != nil {
		return err
	}
	for _, k := range keys {
		status := "active"
		if k.RevokedAt != nil {
			status = "revoked"
		}
		if _, err := fmt.Fprintf(out, "%s client=%s role=%s status=%s created=%s\n", k.Prefix, k.Client, k.Role, status, k.CreatedAt.Format(time.RFC3339)); err != nil {
			return err
		}
	}
	return nil
}
