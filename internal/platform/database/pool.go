package database

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Querier is what repositories need: run statements on the pool or inside a transaction.
type Querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Pool is a PostgreSQL connection pool.
type Pool struct {
	pool *pgxpool.Pool
}

var _ Querier = (*Pool)(nil)

// Open creates the pool. It does not connect eagerly: the process can start while the database is still coming up,
// and Check (readiness) reports the truth until the first connection succeeds.
func Open(ctx context.Context, c Config) (*Pool, error) {
	pc, err := c.poolConfig()
	if err != nil {
		return nil, err
	}
	pool, err := pgxpool.NewWithConfig(ctx, pc)
	if err != nil {
		return nil, Classify(fmt.Errorf("database: open pool: %w", err))
	}
	return &Pool{pool: pool}, nil
}

// Close releases every connection.
func (p *Pool) Close() { p.pool.Close() }

// Check verifies that the database answers a trivial query. Use it as a readiness check.
func (p *Pool) Check(ctx context.Context) error {
	var one int
	if err := p.pool.QueryRow(ctx, "SELECT 1").Scan(&one); err != nil {
		return Classify(err)
	}
	return nil
}

// Exec implements Querier.
func (p *Pool) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	tag, err := p.pool.Exec(ctx, sql, args...)
	return tag, Classify(err)
}

// Query implements Querier.
func (p *Pool) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	rows, err := p.pool.Query(ctx, sql, args...)
	return rows, Classify(err)
}

// QueryRow implements Querier. Errors surface from Scan, where callers can use Classify.
func (p *Pool) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return p.pool.QueryRow(ctx, sql, args...)
}

// Stat returns pool statistics, for metrics.
func (p *Pool) Stat() *pgxpool.Stat { return p.pool.Stat() }

// Retry settings for transactions that fail with a serialization failure or a deadlock.
const (
	txMaxAttempts = 3
	txBaseBackoff = 10 * time.Millisecond
)

// WithTx runs fn inside a transaction and commits when fn returns nil; otherwise it rolls back.
// A serialization failure or deadlock reruns fn up to txMaxAttempts times, so fn must be safe to run again:
// it must not have side effects outside the transaction.
func (p *Pool) WithTx(ctx context.Context, opts pgx.TxOptions, fn func(tx pgx.Tx) error) error {
	return Classify(retry(ctx, txMaxAttempts, waitFor, func() error { return p.runTx(ctx, opts, fn) }))
}

func (p *Pool) runTx(ctx context.Context, opts pgx.TxOptions, fn func(tx pgx.Tx) error) (err error) {
	tx, err := p.pool.BeginTx(ctx, opts)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			// Rollback uses a context that survives cancellation so the connection is never returned mid-transaction.
			if rbErr := tx.Rollback(context.WithoutCancel(ctx)); rbErr != nil && !errors.Is(rbErr, pgx.ErrTxClosed) {
				err = errors.Join(err, rbErr)
			}
		}
	}()
	if err = fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// retry runs op, repeating it while it fails with a retryable error, up to attempts times. wait pauses between
// attempts and must honour ctx. It is a separate function so the policy is testable without a database.
func retry(ctx context.Context, attempts int, wait func(ctx context.Context, d time.Duration) error, op func() error) error {
	var err error
	for attempt := 1; attempt <= attempts; attempt++ {
		if err = op(); err == nil || !isRetryableError(err) || attempt == attempts {
			return err
		}
		if werr := wait(ctx, backoff(attempt)); werr != nil {
			return errors.Join(err, werr)
		}
	}
	return err
}

func isRetryableError(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && isRetryable(pgErr)
}

// backoff grows linearly with the attempt number and adds jitter so competing transactions do not collide again.
func backoff(attempt int) time.Duration {
	base := time.Duration(attempt) * txBaseBackoff
	return base + time.Duration(rand.Int64N(int64(txBaseBackoff))) // #nosec G404 -- jitter, not security sensitive
}

func waitFor(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
