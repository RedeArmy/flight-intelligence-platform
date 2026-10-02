# ADR-035: Worker process, shared service startup and the queue contract

- **Status:** Accepted (2026-10-02)
- **Date:** 2026-10-02
- **Review date:** 2027-04-02 (earlier when the first queue adapter and handlers land in E8)
- **Deciders:** owner (RedeArmy) with E1 plan approval

## Context
ADR-012 puts background work in worker pools inside `cmd/worker`, and ADR-013 defines the `JobQueue` port with a PostgreSQL adapter later. Slice S7 of E1 delivers the worker process and the contract, with no jobs, so the lifecycle, the image, the health checks and the observability are proven before E8 adds behaviour. The design asked for types only and no adapter, to avoid an abstraction without a user.

## Decision
**The worker is a long-running process with the API's lifecycle.** It loads configuration, builds the redacting logger, starts OpenTelemetry (service name `worker`), reads its secrets from the secret store, opens its own PostgreSQL pool (application name `worker`, the same runtime role until E8 grants the queue tables), and serves liveness and readiness. On SIGINT or SIGTERM it reports not ready, stops the listener, flushes telemetry within five seconds and exits 0. It runs no job handlers yet; it logs that no job kinds are registered and idles.

**Startup is shared, not copied.** `internal/platform/service` does what both long-running processes need (configuration, logger, telemetry, secret store, pool, pool and runtime gauges, readiness checks and their gauge). `cmd/api` and `cmd/worker` keep only what is their own. The API was moved onto it in this slice; its unit and integration tests, including the end-to-end telemetry test, pass unchanged. A failed start leaves nothing open, and closing a partly built service is safe.

**Probes on a small listener.** The worker serves only `GET /healthz` and `GET /readyz`, with the same bodies as the API's probes, through the shared base chain (request ID, tracing, access log, panic recovery, security headers). It exposes no API route and no metrics endpoint (ADR-033). The listener is `WORKER_HEALTH_ADDR`, loopback by default (`127.0.0.1:8082`); the container's health check reads the same key. The HTTP server gained an optional operator listener for this: a nil operator handler runs a single listener.

**Health check.** The image has no shell, so `worker healthcheck` probes itself, like `api healthcheck`. The probe moved to `internal/platform/cli` and takes the address key as a parameter, so both services share it.

**Queue contract (`internal/platform/queue`).** Types and rules only, with no adapter and no consumer loop:
- `Job`: queue, kind, payload (at most 64 KiB, no secrets), idempotency key, run-at, attempt limits and trace metadata. `Validate` bounds names, sizes and keys so they are safe to log and to store.
- Delivery is at least once and handlers must be idempotent. A handler returning nil acknowledges; any other error retries with backoff until the job's attempts are exhausted; `Permanent(err)` sends the job to the dead-letter queue at once; an exhausted job goes there too.
- `Policy.Delay` computes exponential backoff with a cap and jitter over the upper half, so jobs that failed together do not retry together.
- `JobQueue` is the port (`Publish`, `Consume`), and `Handler` is the single-method handler interface.

## Alternatives considered
- Copying the API's startup into the worker: about 100 duplicated lines with two places to keep correct (Sonar already tracks duplication).
- Defining no queue contract until E8: the delivery rules would then be decided inside the adapter, where a second implementation could not be checked against them.
- A worker without a listener and a container health check based on the process: cannot tell a stuck process from a healthy one, and gives operators no readiness.
- Serving the API's whole handler from the worker: exposes routes that make no sense there.
- Writing the consumer loop now: nothing exercises it, so it would be untested design.

## Consequences
+ The worker, its image, Compose service, probes, metrics and shutdown are proven before any job exists, and E8 only adds handlers and the adapter.
+ The API and the worker cannot drift in how they start, log or shut down.
+ Handlers and adapters written later share one definition of acknowledge, retry and dead-letter.
- Until E8 the worker only idles; its readiness proves PostgreSQL connectivity, nothing about jobs.
- The worker uses the runtime database role, which has no queue table grants yet; E8 adds them with the adapter's migration.
- The backoff numbers (5 seconds doubling to 15 minutes, 5 attempts) are starting values to validate under real load.

## Rejected options
A worker that exposes API routes or metrics endpoints; handlers that decide delivery semantics per job kind; retrying errors the job itself caused; secrets or personal data in job payloads or metadata.
