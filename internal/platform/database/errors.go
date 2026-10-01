package database

import (
	"context"
	stderrors "errors"
	"net"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	sharederrors "github.com/RedeArmy/flight-intelligence-platform/internal/shared/errors"
)

// safeDBMessage is the only text a caller sees for an unclassified database failure; the cause stays internal.
const safeDBMessage = "database error"

// Codes returned by Classify. Bounded contexts translate them into their own codes when a more specific one exists.
const (
	CodeDBConflict      = "DB_CONFLICT"
	CodeDBInvalidData   = "DB_INVALID_DATA"
	CodeDBUnavailable   = "DB_UNAVAILABLE"
	CodeDBTimeout       = "DB_TIMEOUT"
	CodeDBPrivilege     = "DB_PRIVILEGE"
	CodeDBSerialization = "DB_SERIALIZATION_FAILURE"
	CodeDBError         = "DB_ERROR"
)

// SQLSTATE codes the platform reacts to (https://www.postgresql.org/docs/current/errcodes-appendix.html).
const (
	sqlStateSerializationFailure  = "40001"
	sqlStateDeadlockDetected      = "40P01"
	sqlStateQueryCanceled         = "57014"
	sqlStateUniqueViolation       = "23505"
	sqlStateForeignKeyViolation   = "23503"
	sqlStateInsufficientPrivilege = "42501"
)

// Classify turns a database error into a classified platform error. The original error is kept as the cause, so it is
// logged but never shown to clients. Errors that are already classified pass through unchanged.
func Classify(err error) error {
	if err == nil {
		return nil
	}
	var classified *sharederrors.Error
	if stderrors.As(err, &classified) {
		return err
	}
	if stderrors.Is(err, pgx.ErrNoRows) {
		return sharederrors.Wrap(sharederrors.KindNotFound, sharederrors.CodeNotFound, "not found", err)
	}
	if stderrors.Is(err, context.DeadlineExceeded) {
		return sharederrors.Wrap(sharederrors.KindTimeout, CodeDBTimeout, "database operation timed out", err)
	}
	var pgErr *pgconn.PgError
	if stderrors.As(err, &pgErr) {
		return classifyPg(pgErr, err)
	}
	if isConnectionError(err) {
		return sharederrors.Wrap(sharederrors.KindUnavailable, CodeDBUnavailable, "database unavailable", err)
	}
	return sharederrors.Wrap(sharederrors.KindInternal, CodeDBError, safeDBMessage, err)
}

func classifyPg(pgErr *pgconn.PgError, cause error) error {
	switch {
	case pgErr.Code == sqlStateUniqueViolation, pgErr.Code == sqlStateForeignKeyViolation:
		return sharederrors.Wrap(sharederrors.KindConflict, CodeDBConflict, "conflicts with existing data", cause)
	case pgErr.Code == sqlStateQueryCanceled:
		return sharederrors.Wrap(sharederrors.KindTimeout, CodeDBTimeout, "database operation timed out", cause)
	case pgErr.Code == sqlStateInsufficientPrivilege:
		// A privilege error means the code or the grants are wrong, not that the caller did anything wrong.
		return sharederrors.Wrap(sharederrors.KindInternal, CodeDBPrivilege, safeDBMessage, cause)
	case isRetryable(pgErr):
		return sharederrors.Wrap(sharederrors.KindUnavailable, CodeDBSerialization, "temporarily unavailable, retry", cause)
	case strings.HasPrefix(pgErr.Code, "22"), strings.HasPrefix(pgErr.Code, "23"):
		// Class 22 (data exception) and the remaining class 23 (not null, check): the data was not acceptable.
		return sharederrors.Wrap(sharederrors.KindInvalid, CodeDBInvalidData, "data not acceptable", cause)
	case strings.HasPrefix(pgErr.Code, "08"), strings.HasPrefix(pgErr.Code, "53"), strings.HasPrefix(pgErr.Code, "57P"):
		// Connection exception, insufficient resources, operator intervention (shutdown, restart).
		return sharederrors.Wrap(sharederrors.KindUnavailable, CodeDBUnavailable, "database unavailable", cause)
	}
	return sharederrors.Wrap(sharederrors.KindInternal, CodeDBError, safeDBMessage, cause)
}

// isRetryable reports whether the failed transaction can simply be run again.
func isRetryable(pgErr *pgconn.PgError) bool {
	return pgErr.Code == sqlStateSerializationFailure || pgErr.Code == sqlStateDeadlockDetected
}

func isConnectionError(err error) bool {
	var netErr net.Error
	var connectErr *pgconn.ConnectError
	return stderrors.As(err, &netErr) || stderrors.As(err, &connectErr) || pgconn.Timeout(err)
}
