package httpserver

import (
	"crypto/rand"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"regexp"
	"runtime/debug"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"

	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/observability/logging"
	sharederrors "github.com/RedeArmy/flight-intelligence-platform/internal/shared/errors"
)

const requestIDHeader = "X-Request-Id"

// inboundRequestID limits what a client may supply as its own request ID, so IDs stay safe in logs and headers.
var inboundRequestID = regexp.MustCompile(`^[A-Za-z0-9._-]{8,64}$`)

// requestID accepts a well-formed inbound X-Request-Id or generates one, echoes it on the response, and puts it and
// the logger into the request context so every log line and error body carries it.
func requestID(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := r.Header.Get(requestIDHeader)
			if !inboundRequestID.MatchString(id) {
				id = newRequestID()
			}
			w.Header().Set(requestIDHeader, id)
			ctx := withLogger(logging.WithRequestID(r.Context(), id), logger)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func newRequestID() string {
	return "req_" + strings.ToLower(rand.Text())[:16]
}

// recoverer turns a panic into a 500 error response and logs it with a stack trace. http.ErrAbortHandler is
// re-raised because net/http uses it to abort a response on purpose.
func recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer recoverPanic(w, r)
		next.ServeHTTP(w, r)
	})
}

// recoverPanic must be deferred directly so that recover() sees the panic.
func recoverPanic(w http.ResponseWriter, r *http.Request) {
	rec := recover()
	if rec == nil {
		return
	}
	if rec == http.ErrAbortHandler { //nolint:errorlint // net/http compares the sentinel by identity
		panic(rec)
	}
	loggerFrom(r.Context()).ErrorContext(r.Context(), "panic recovered",
		"panic", fmt.Sprint(rec), "stack", string(debug.Stack()))
	WriteError(w, r, sharederrors.Internal(sharederrors.CodeInternal, "internal error"))
}

// accessLog logs one record per request: method, route template (never the raw path, to bound cardinality), status,
// duration and size. It never logs headers, query strings or bodies.
func accessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := chimw.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)

		route := "unmatched"
		if rc := chi.RouteContext(r.Context()); rc != nil && rc.RoutePattern() != "" {
			route = rc.RoutePattern()
		}
		status := ww.Status()
		if status == 0 {
			status = http.StatusOK
		}
		loggerFrom(r.Context()).Log(r.Context(), accessLevel(route, status), "request",
			"method", r.Method,
			"route", route,
			"status", status,
			"duration_ms", time.Since(start).Milliseconds(),
			"bytes", ww.BytesWritten(),
			"remote_ip", remoteHost(r.RemoteAddr),
		)
	})
}

// accessLevel keeps probe traffic quiet and raises server errors.
func accessLevel(route string, status int) slog.Level {
	switch {
	case status >= http.StatusInternalServerError:
		return slog.LevelError
	case route == "/healthz" || route == "/readyz":
		return slog.LevelDebug
	}
	return slog.LevelInfo
}

func remoteHost(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return host
}

// securityHeaders sets conservative response headers for a JSON API. HSTS is left to the TLS-terminating edge.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Cache-Control", "no-store")
		h.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		h.Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

// bodyLimit caps the request body size; reading past the cap fails with *http.MaxBytesError, which WriteError maps to 413.
func bodyLimit(maxBytes int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
			next.ServeHTTP(w, r)
		})
	}
}
