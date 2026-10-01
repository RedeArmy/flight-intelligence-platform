// Package migrate applies the embedded SQL migrations with golang-migrate (ADR-003, ADR-031).
//
// It runs as the schema-owning role (fip_migrator), never as the runtime role. golang-migrate takes a database-wide
// advisory lock, so concurrent runs are safe.
package migrate

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/url"
	"strconv"
	"strings"

	gomigrate "github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5" // registers the pgx5 database driver
	"github.com/golang-migrate/migrate/v4/source/iofs"

	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/database"
)

// Runner applies migrations to one database.
type Runner struct {
	cfg  database.Config
	fsys fs.FS
	log  *slog.Logger
}

// New returns a Runner. fsys holds NNNN_name.up.sql and NNNN_name.down.sql files at its root.
func New(cfg database.Config, fsys fs.FS, log *slog.Logger) *Runner {
	return &Runner{cfg: cfg, fsys: fsys, log: log}
}

// Up applies every pending migration. Being up to date is not an error.
func (r *Runner) Up(ctx context.Context) error {
	return r.run(ctx, func(m *gomigrate.Migrate) error { return m.Up() })
}

// Down rolls back the given number of migrations. It exists for local development; production rolls forward.
func (r *Runner) Down(ctx context.Context, steps int) error {
	if steps < 1 {
		return errors.New("migrate: steps must be at least 1")
	}
	return r.run(ctx, func(m *gomigrate.Migrate) error { return m.Steps(-steps) })
}

// Version returns the current version and whether the last migration left the database dirty (half applied).
// A database with no migrations applied reports version 0.
func (r *Runner) Version(ctx context.Context) (version uint, dirty bool, err error) {
	err = r.run(ctx, func(m *gomigrate.Migrate) error {
		v, d, verr := m.Version()
		if errors.Is(verr, gomigrate.ErrNilVersion) {
			return nil
		}
		version, dirty = v, d
		return verr
	})
	return version, dirty, err
}

// run opens the migrator, runs op, and closes it. ErrNoChange is success. Cancelling ctx stops between migrations.
func (r *Runner) run(ctx context.Context, op func(*gomigrate.Migrate) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	dsn, err := r.dsn()
	if err != nil {
		return err
	}
	src, err := iofs.New(r.fsys, ".")
	if err != nil {
		return fmt.Errorf("migrate: read migrations: %w", err)
	}
	m, err := gomigrate.NewWithSourceInstance("iofs", src, dsn)
	if err != nil {
		return fmt.Errorf("migrate: connect: %w", sanitize(err, dsn))
	}
	m.Log = slogLogger{r.log}

	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			select {
			case m.GracefulStop <- true:
			case <-done:
			}
		case <-done:
		}
	}()

	opErr := op(m)
	srcErr, dbErr := m.Close()
	if opErr != nil && !errors.Is(opErr, gomigrate.ErrNoChange) {
		return fmt.Errorf("migrate: %w", sanitize(opErr, dsn))
	}
	return errors.Join(srcErr, dbErr)
}

// dsn builds the golang-migrate connection string. It contains the password and must never be logged.
func (r *Runner) dsn() (string, error) {
	if err := r.cfg.Validate(); err != nil {
		return "", err
	}
	q := url.Values{}
	q.Set("sslmode", r.cfg.SSLMode)
	u := url.URL{
		Scheme:   "pgx5",
		User:     url.UserPassword(r.cfg.User, r.cfg.Password.Reveal()),
		Host:     net.JoinHostPort(r.cfg.Host, strconv.Itoa(r.cfg.Port)),
		Path:     "/" + r.cfg.Name,
		RawQuery: q.Encode(),
	}
	return u.String(), nil
}

// sanitize removes the connection string, and so the password, from an error message.
func sanitize(err error, dsn string) error {
	if err == nil || dsn == "" {
		return err
	}
	if strings.Contains(err.Error(), dsn) {
		return errors.New(strings.ReplaceAll(err.Error(), dsn, "<dsn redacted>"))
	}
	return err
}

// slogLogger adapts slog to golang-migrate's logger interface.
type slogLogger struct{ log *slog.Logger }

func (l slogLogger) Printf(format string, v ...any) {
	if l.log != nil {
		l.log.Info(strings.TrimSpace(fmt.Sprintf(format, v...)), "component", "migrate")
	}
}

func (l slogLogger) Verbose() bool { return false }
