// Package errors is the platform's classified error model (ADR-026, Constitution section 69).
//
// An *Error carries a Kind (how to treat it), a stable Code (what clients and tests match on), a
// client-safe Message, optional Details, and a Cause that is logged but never shown to clients.
// Mapping a Kind to an HTTP status happens only in the HTTP layer; this package has no transport knowledge.
package errors

import (
	"context"
	stderrors "errors"
	"fmt"
	"maps"
	"regexp"
)

// Kind classifies an error by how callers should react to it.
type Kind uint8

// Kinds. The zero value is KindInternal so an unclassified error is never mistaken for a client mistake.
const (
	KindInternal Kind = iota
	KindInvalid
	KindUnauthenticated
	KindForbidden
	KindNotFound
	KindConflict
	KindRateLimited
	KindUnavailable
	KindTimeout
)

var kindNames = map[Kind]string{
	KindInternal:        "internal",
	KindInvalid:         "invalid",
	KindUnauthenticated: "unauthenticated",
	KindForbidden:       "forbidden",
	KindNotFound:        "not_found",
	KindConflict:        "conflict",
	KindRateLimited:     "rate_limited",
	KindUnavailable:     "unavailable",
	KindTimeout:         "timeout",
}

func (k Kind) String() string {
	if name, ok := kindNames[k]; ok {
		return name
	}
	return "unknown"
}

// Generic codes shared by all contexts. Context-specific codes live in their own packages.
const (
	CodeInternal        = "INTERNAL"
	CodeInvalidRequest  = "INVALID_REQUEST"
	CodeUnauthenticated = "UNAUTHENTICATED"
	CodeForbidden       = "FORBIDDEN"
	CodeNotFound        = "NOT_FOUND"
	CodeConflict        = "CONFLICT"
	CodeRateLimited     = "RATE_LIMITED"
	CodeUnavailable     = "UNAVAILABLE"
	CodeTimeout         = "TIMEOUT"
	CodeCanceled        = "CANCELED"
)

const publicInternalMessage = "internal error"

var codePattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]{1,63}$`)

// ValidCode reports whether code is an UPPER_SNAKE_CASE identifier of 2 to 64 characters.
func ValidCode(code string) bool { return codePattern.MatchString(code) }

// Error is a classified error. Treat values as immutable; use the With* methods to derive variants.
type Error struct {
	Kind    Kind
	Code    string
	Message string         // safe to show to clients
	Details map[string]any // safe to show to clients
	Cause   error          // logged, never shown to clients
}

// New builds an Error. It panics when code is not a valid code, because that is a programming error
// that must surface the first time the (usually package-level) error is created.
func New(kind Kind, code, message string) *Error {
	if !ValidCode(code) {
		panic(fmt.Sprintf("errors: invalid error code %q (want UPPER_SNAKE_CASE, 2-64 chars)", code))
	}
	return &Error{Kind: kind, Code: code, Message: message}
}

// Wrap builds an Error that records cause.
func Wrap(kind Kind, code, message string, cause error) *Error {
	e := New(kind, code, message)
	e.Cause = cause
	return e
}

// Convenience constructors, one per kind.

func Internal(code, message string) *Error        { return New(KindInternal, code, message) }
func Invalid(code, message string) *Error         { return New(KindInvalid, code, message) }
func Unauthenticated(code, message string) *Error { return New(KindUnauthenticated, code, message) }
func Forbidden(code, message string) *Error       { return New(KindForbidden, code, message) }
func NotFound(code, message string) *Error        { return New(KindNotFound, code, message) }
func Conflict(code, message string) *Error        { return New(KindConflict, code, message) }
func RateLimited(code, message string) *Error     { return New(KindRateLimited, code, message) }
func Unavailable(code, message string) *Error     { return New(KindUnavailable, code, message) }
func Timeout(code, message string) *Error         { return New(KindTimeout, code, message) }

// Error implements the error interface. It includes the cause, so use it for logs, never for clients.
func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	s := e.Code
	if e.Message != "" {
		s += ": " + e.Message
	}
	if e.Cause != nil {
		s += ": " + e.Cause.Error()
	}
	return s
}

// Unwrap exposes the cause to errors.Is and errors.As.
func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// Is reports whether target is an *Error with the same Kind and Code, so sentinel errors
// declared once at package level match any instance derived from them.
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	if !ok || e == nil || t == nil {
		return false
	}
	return e.Kind == t.Kind && e.Code == t.Code
}

// WithCause returns a copy of e with the given cause.
func (e *Error) WithCause(cause error) *Error {
	c := *e
	c.Details = maps.Clone(e.Details)
	c.Cause = cause
	return &c
}

// WithDetails returns a copy of e with details merged in (later keys win). e is not modified.
func (e *Error) WithDetails(details map[string]any) *Error {
	c := *e
	c.Details = maps.Clone(e.Details)
	if c.Details == nil {
		c.Details = make(map[string]any, len(details))
	}
	maps.Copy(c.Details, details)
	return &c
}

// WithMessage returns a copy of e with a different client-safe message.
func (e *Error) WithMessage(message string) *Error {
	c := *e
	c.Details = maps.Clone(e.Details)
	c.Message = message
	return &c
}

// classify resolves err to the *Error that best describes it, adding context errors that are not *Error.
func classify(err error) *Error {
	if err == nil {
		return nil
	}
	var e *Error
	switch {
	case stderrors.As(err, &e):
		return e
	case stderrors.Is(err, context.DeadlineExceeded):
		return Timeout(CodeTimeout, "request timed out")
	case stderrors.Is(err, context.Canceled):
		return Unavailable(CodeCanceled, "request canceled")
	}
	return Internal(CodeInternal, publicInternalMessage)
}

// KindOf returns the Kind of err; unclassified errors are KindInternal.
func KindOf(err error) Kind {
	if e := classify(err); e != nil {
		return e.Kind
	}
	return KindInternal
}

// CodeOf returns the stable code of err; unclassified errors yield CodeInternal.
func CodeOf(err error) string {
	if e := classify(err); e != nil {
		return e.Code
	}
	return CodeInternal
}

// PublicMessage returns a message that is safe to show to clients. For unclassified errors it is
// always the generic "internal error"; a cause is never included.
func PublicMessage(err error) string {
	e := classify(err)
	if e == nil {
		return publicInternalMessage
	}
	if e.Kind == KindInternal {
		return publicInternalMessage
	}
	return e.Message
}

// DetailsOf returns a copy of the client-safe details of err, or nil.
func DetailsOf(err error) map[string]any {
	e := classify(err)
	if e == nil || e.Kind == KindInternal {
		return nil
	}
	return maps.Clone(e.Details)
}
