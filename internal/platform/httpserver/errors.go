// Package httpserver is the HTTP transport: routing generated from the OpenAPI contract, the middleware
// chain, error mapping, health probes and listener lifecycle (ADR-005, ADR-026).
//
// It is the only place that knows how a classified error becomes an HTTP response.
package httpserver

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/observability/logging"
	sharederrors "github.com/RedeArmy/flight-intelligence-platform/internal/shared/errors"
)

// Codes that only exist at the HTTP layer.
const (
	CodeMethodNotAllowed = "METHOD_NOT_ALLOWED"
	CodePayloadTooLarge  = "PAYLOAD_TOO_LARGE"
)

// statusFor maps an error Kind to its HTTP status.
func statusFor(kind sharederrors.Kind) int {
	switch kind {
	case sharederrors.KindInvalid:
		return http.StatusBadRequest
	case sharederrors.KindUnauthenticated:
		return http.StatusUnauthorized
	case sharederrors.KindForbidden:
		return http.StatusForbidden
	case sharederrors.KindNotFound:
		return http.StatusNotFound
	case sharederrors.KindConflict:
		return http.StatusConflict
	case sharederrors.KindRateLimited:
		return http.StatusTooManyRequests
	case sharederrors.KindUnavailable:
		return http.StatusServiceUnavailable
	case sharederrors.KindTimeout:
		return http.StatusGatewayTimeout
	}
	return http.StatusInternalServerError
}

// errorBody is the error envelope defined by the OpenAPI contract (ErrorResponse).
type errorBody struct {
	Error errorPayload `json:"error"`
}

type errorPayload struct {
	Code      string         `json:"code"`
	Message   string         `json:"message"`
	RequestID string         `json:"requestId"`
	Details   map[string]any `json:"details,omitempty"`
}

type loggerKey struct{}

func withLogger(ctx context.Context, l *slog.Logger) context.Context {
	return context.WithValue(ctx, loggerKey{}, l)
}

var discardLogger = slog.New(slog.DiscardHandler)

// loggerFrom returns the request-scoped logger, or a logger that discards output.
func loggerFrom(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(loggerKey{}).(*slog.Logger); ok {
		return l
	}
	return discardLogger
}

// WriteError writes err as the standard error envelope. Internal detail is never returned: clients see the
// stable code and the client-safe message only. Server-side failures are logged with their cause.
func WriteError(w http.ResponseWriter, r *http.Request, err error) {
	var tooLarge *http.MaxBytesError
	if stderrors.As(err, &tooLarge) {
		writeJSONError(w, r, http.StatusRequestEntityTooLarge, CodePayloadTooLarge, "request body too large", nil)
		return
	}

	kind := sharederrors.KindOf(err)
	status := statusFor(kind)
	if status >= http.StatusInternalServerError {
		loggerFrom(r.Context()).ErrorContext(r.Context(), "request failed",
			"code", sharederrors.CodeOf(err), "kind", kind.String(), "err", err)
	}
	if kind == sharederrors.KindRateLimited {
		setRetryAfter(w, sharederrors.DetailsOf(err))
	}
	writeJSONError(w, r, status, sharederrors.CodeOf(err), sharederrors.PublicMessage(err), sharederrors.DetailsOf(err))
}

func setRetryAfter(w http.ResponseWriter, details map[string]any) {
	if secs, ok := details["retryAfterSeconds"].(int); ok && secs >= 0 {
		w.Header().Set("Retry-After", strconv.Itoa(secs))
	}
}

func writeJSONError(w http.ResponseWriter, r *http.Request, status int, code, message string, details map[string]any) {
	body := errorBody{Error: errorPayload{
		Code:      code,
		Message:   message,
		RequestID: logging.RequestID(r.Context()),
		Details:   details,
	}}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		loggerFrom(r.Context()).DebugContext(r.Context(), "writing error response failed", "err", err)
	}
}
