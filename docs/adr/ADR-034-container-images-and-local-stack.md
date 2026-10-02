# ADR-034: Container images and the local stack

- **Status:** Accepted (2026-10-02)
- **Date:** 2026-10-02
- **Review date:** 2027-04-02 (earlier when the worker image is added or a deployment vendor is chosen)
- **Deciders:** owner (RedeArmy) with E1 plan approval

## Context
Slice S6 of E1 packages the services and completes the local stack (ADR-025 amendment, ADR-028): everything runs on one machine through Docker Compose, with no cloud vendor. The images must be small, reproducible and safe to promote later, and the stack must let a developer see logs, traces and metrics (ADR-019, ADR-033) and prove that backups can be restored (ADR-023).

## Decision
**One Dockerfile, one target per purpose.** `api` contains only the API. `worker` contains only the worker. `tools` contains `cmd/migrate` and `cmd/keyctl`, used for one-off tasks. The API image has no administration tool, so a compromised API container cannot issue keys or change the schema even in principle (it also lacks the passwords, which are not mounted there). The worker target arrived with the worker (ADR-035).

**Build.** Multi-stage on `golang:1.27.1-alpine`, static binaries (`CGO_ENABLED=0`), `-trimpath` and stripped symbols, `GOTOOLCHAIN=local` so the build never downloads another Go (ADR-030), `-mod=readonly`. Dependencies are downloaded in their own layer so source changes do not refetch them. The build context excludes secrets, VCS data, build output and documentation (`.dockerignore`), so nothing sensitive can reach a layer.

**Runtime.** `gcr.io/distroless/static-debian12:nonroot`: no shell, no package manager, no libc; user 65532. Base images are pinned by digest, written out in each `FROM` rather than through `ARG`, because Dependabot's docker ecosystem only updates literal references (it now watches the Dockerfile as well as Compose).

**Health check.** The runtime image has no shell or `curl`, so the binary checks itself: `api healthcheck` performs `GET /healthz` on the loopback address, with a three-second timeout, using the port of `HTTP_ADDR`. It probes liveness, not readiness: a database outage must restart nothing, readiness is what `/readyz` and the metrics report.

**Container hardening in Compose.** The containers built here run with a read-only root filesystem (a `tmpfs` at `/tmp`), all capabilities dropped, `no-new-privileges`, and the unprivileged user. Secrets are Docker secrets mounted as files under `/run/secrets` and read by the same local secret store as on the host (`SECRETS_DIR`), never environment variables: `docker inspect` of the API container shows none. On a Linux host the files in `./secrets` are mode 0600 and owned by the developer, which a different user inside the container cannot read; `FIP_UID` and `FIP_GID` make the containers run as the developer.

**Profiles.** Services without a profile are the infrastructure (`make dev`): PostgreSQL, Redis, the collector, Jaeger and Prometheus. Profile `app` adds the migration job, the API and the worker (`make stack`). Profile `tools` is the operator tool, run on demand (`make keyctl ARGS="key list"`). Profile `test` is the throw-away integration-test services. Every published port is bound to `127.0.0.1`.

**Migrations stay a separate step.** The `migrate` job runs as the schema-owning role and exits; the API starts only after it succeeds and never migrates itself (ADR-031).

**Redis** in the stack requires a password from a secret file and persists nothing (ADR-004). The test Redis keeps its own service without a password because it is throw-away and loopback only.

**Observability stack.** The API sends OTLP/HTTP to the collector; the collector keeps errors and slow traces (tail sampling), sends traces to Jaeger (in-memory storage, a local viewer) and exposes metrics that Prometheus scrapes (seven days kept). The API has no `/metrics` endpoint.

**Restore drill (ADR-023).** `make restore-drill` dumps the running local database with `pg_dump`, restores it into a scratch database, compares every table's row count and the migration version, prints the time of each step, and removes the scratch database and the dump even when it fails. It runs inside the container with the superuser password read from the Docker secret, so the password never appears on a host command line. A restore that differs from the source, or a scratch database that cannot be removed, makes it fail.

## Alternatives considered
- One image with every binary: simpler to publish, but the API image would contain tools that issue keys and change the schema.
- A shell-based health check (`wget`, `curl`): needs a shell and extra binaries in the runtime image; the self-check keeps it empty.
- `gcr.io/distroless/static` by tag only, or `alpine`: a moved tag changes what runs, and a shell widens what an attacker gets.
- Running migrations when the API starts: blocked by ADR-031.
- A `/metrics` endpoint instead of the collector: a second path and another route to protect (ADR-033).

## Consequences
+ Small images (about 43 MB for the API) with a short, reviewable content list, built reproducibly.
+ The local stack shows logs, traces and metrics end to end, and backups are proven restorable.
- Images are not scanned or published yet; image build, vulnerability scan and SBOM belong to the CI slice (S8).
- The Dockerfile pins base images by digest, so updates arrive as Dependabot pull requests that need review.
- Compose secrets are read from `./secrets`; a Linux host needs `FIP_UID`/`FIP_GID` (documented in the README).
- The `restore-drill` verifies row counts and the migration version, not every value; it proves the dump restores, not that the data is semantically intact.

## Rejected options
Secrets in environment variables or image layers; running containers as root or with a writable root filesystem; publishing infrastructure ports on all interfaces; a health check that depends on the database.
