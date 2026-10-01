# ADR-026: Error model, configuration and logging conventions

- **Status:** Accepted (2026-10-01)
- **Date:** 2026-10-01
- **Review date:** 2027-04-01 (earlier if a trigger named in Consequences occurs)
- **Deciders:** owner (RedeArmy) with E1 plan approval

## Context
E1 slice S1 introduces the first shared code: how errors are classified and exposed, how configuration is loaded and validated, and how logs avoid leaking credentials (Constitution sections 46, 69, 70; SR-04, SR-09, SR-19). These conventions are used by every later slice, so they need to be explicit and testable. The HTTP-specific parts of the original plan (middleware order, listeners, readiness) are decided with slice S2 and recorded as an addendum here.

## Decision
**Errors** (`internal/shared/errors`). A single `*Error` carries a `Kind` (internal, invalid, unauthenticated, forbidden, not_found, conflict, rate_limited, unavailable, timeout), a stable UPPER_SNAKE_CASE `Code`, a client-safe `Message`, optional client-safe `Details`, and a `Cause` that is logged but never returned. The zero Kind is `internal`, so an unclassified error is never mistaken for a client mistake. `New` panics on an invalid code (a programming error that surfaces at package initialisation). Sentinel errors match with `errors.Is` by Kind and Code. `PublicMessage` and `DetailsOf` return generic output for internal errors. Mapping Kind to HTTP status lives only in the HTTP layer; domain and application code never import `net/http`.

**Configuration** (`internal/platform/config`). Typed structs loaded from environment variables through an injectable `Lookup`, stdlib only. Loading is fail-fast and reports every problem at once; messages never contain values. An empty default means required. `APP_ENV` is required (no default), so local-only behaviour can never be enabled by omission. Cross-field rules are checked after parsing (for example, text logs only in local/test, operator listener distinct from the public one). A git-ignored `.env` file is read only when `APP_ENV` is unset or local/test, cannot select staging or production, and never overrides real environment variables; it is opened with `os.OpenRoot` to prevent path traversal. Secrets are never configuration keys; they use the `SecretStore` port (ADR-018) and the `secret.Secret` type. Every key must be documented in `docs/operations/configuration.md`, enforced by a test in both directions.

**Logging** (`internal/platform/observability/logging`). `log/slog` with a JSON handler (text in local/test). Every record carries service, env and version; request, correlation and trace identifiers come from the context (trace IDs through an injected extractor so the logging package does not depend on the OpenTelemetry SDK). A `ReplaceAttr` hook redacts sensitive keys (matched broadly by key name), resolves `secret.Secret` values, and scrubs recognisable credentials in strings, messages and error texts: platform API keys, bearer and basic tokens, URL passwords, and `password=`/`token:`-style pairs. A leak test logs known secrets through every code path and asserts none reach the output.

**Time and secrets.** `internal/shared/clock` provides an injectable `Nower` interface (the Go name for a single-method `Now` interface) with a concurrency-safe `Fake`. `internal/shared/secret` provides `Secret`, which prints, marshals and logs as `[REDACTED]` and is read only through `Reveal`.

## Alternatives considered
- Sentinel errors plus string matching: not classifiable and easy to leak causes.
- A configuration library (viper, envconfig): more dependencies and implicit behaviour for a small, security-sensitive surface.
- Allowing `.env` in every environment: risks local defaults reaching production.
- Redaction only by key name: misses secrets embedded in messages, URLs and error strings.

## Consequences
+ One classification model from domain to HTTP, no accidental leakage of causes, fail-fast configuration, documented keys, and tested log redaction.
- Redaction is defence in depth, not a guarantee: a secret written as plain prose with no recognisable shape (for example `password hunter2`) cannot be detected. The rule stays: never put a secret in a log message; pass a `secret.Secret` attribute instead. Broad key matching can over-redact (for example a key named `tokens_used`); this is accepted.
- Adding a configuration key requires a documentation row in the same change.
- `errors.New` panicking on a bad code means invalid codes fail at startup, not at runtime.

## Rejected options
Logging full provider responses; configuration read from files in production; secrets as plain strings in configuration structs.

## Addendum (E1 S2, 2026-10-01): HTTP conventions
- **Error mapping.** Kind to status lives only in `internal/platform/httpserver`: invalid 400, unauthenticated 401, forbidden 403, not_found 404, conflict 409, rate_limited 429, unavailable 503, timeout 504, internal 500. HTTP-only codes: `METHOD_NOT_ALLOWED` (405), `PAYLOAD_TOO_LARGE` (413). The envelope is `{"error":{"code","message","requestId","details"}}`, identical to the OpenAPI `ErrorResponse`; a test keeps them in step. Server-side failures are logged with their cause; clients never see it.
- **Middleware order.** request ID, access log, panic recovery, security headers, body limit, then per route the access policy (authenticate, authorise). Access log sits outside recovery so a recovered panic is logged as a 500. Rate limiting (S4) and OpenTelemetry (S5) slot into this chain.
- **Request IDs.** An inbound `X-Request-Id` is accepted only if it matches `^[A-Za-z0-9._-]{8,64}$`; otherwise a `req_` ID is generated. It is echoed on every response and appears in every log line and error body.
- **Access control.** An explicit route-policy table (public, or required permission) is deny-by-default: a registered route missing from it answers 500 `ROUTE_POLICY_MISSING`. A test cross-checks the table, the registered routes and the OpenAPI contract (`security: []` means public, `x-permission` must match) in both directions. The default `Authenticator` rejects everything until the API-key authenticator lands in S4.
- **Listeners.** Public and operator listeners are separate sockets with separate routers; operator routes can never be reached through the public port (SR-21). Operator address defaults to loopback.
- **Probes.** `/healthz` (liveness, no dependencies) and `/readyz` (critical check failure or draining gives 503; an optional dependency failing gives `degraded` with 200) are public and unversioned.
- **Shutdown.** SIGINT/SIGTERM flips readiness to not ready, stops accepting connections, gives in-flight requests `HTTP_SHUTDOWN_TIMEOUT` to finish, then force-closes and reports an error.
