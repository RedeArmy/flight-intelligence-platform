package httpserver

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/httpserver/openapi"
)

// HealthDeps are the dependencies of the health-only handler.
type HealthDeps struct {
	Logger    *slog.Logger
	Health    *Health
	Telemetry *Instrumentation // nil disables traces and metrics (tests only)
}

// NewHealthHandler serves only /healthz and /readyz, with the same bodies as the API's probes. It is for processes
// that have no public API, such as the worker: they still need liveness and readiness for the container runtime and
// for operators, and must not expose any API route. It shares the base chain (request ID, tracing, access log, panic
// recovery, security headers).
func NewHealthHandler(d HealthDeps) http.Handler {
	r := newBaseRouter(d.Logger, d.Telemetry)
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, d.Logger, http.StatusOK, openapi.Liveness{Status: openapi.Ok})
	})
	r.Get("/readyz", func(w http.ResponseWriter, req *http.Request) {
		state, checks := d.Health.Readiness(req.Context())
		body := openapi.Readiness{Status: openapi.ReadinessStatus(state)}
		if len(checks) > 0 {
			body.Checks = &checks
		}
		status := http.StatusOK
		if state == StateNotReady {
			status = http.StatusServiceUnavailable
		}
		writeJSON(w, d.Logger, status, body)
	})
	return r
}

func writeJSON(w http.ResponseWriter, logger *slog.Logger, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		logger.Debug("writing response failed", "err", err)
	}
}
