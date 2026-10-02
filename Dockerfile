# Container images for the Flight Intelligence Platform (ADR-034). One Dockerfile, one target per purpose:
#
#   api     the public HTTP API; contains no administration tool
#   tools   cmd/migrate and cmd/keyctl, used for one-off tasks (migrations, key administration)
#
#   docker build --target api   -t fip-api:local .
#   docker build --target tools -t fip-tools:local .
#
# Base images are pinned by digest so a build is reproducible and a moved tag cannot change what runs. The names are
# written out in each FROM (not through ARG) because Dependabot's docker ecosystem only updates literal references.

# --- Build ---------------------------------------------------------------------------------------------------------
FROM golang:1.27.1-alpine@sha256:8a5910f31396cd4d89662f56c68b3ae31d374308270a1c3bd96672ee5ed43414 AS build
WORKDIR /src
# Static binaries (no C library in the runtime image), module downloads verified by go.sum, the toolchain pinned to the
# image's Go (ADR-030) so the build never downloads another one.
ENV CGO_ENABLED=0 GOFLAGS=-mod=readonly GOTOOLCHAIN=local
COPY go.mod go.sum ./
RUN go mod download
COPY . .

FROM build AS build-api
# -trimpath removes local paths from the binary; -s -w drop the symbol table and debug data.
RUN go build -trimpath -ldflags="-s -w" -o /out/api ./cmd/api

FROM build AS build-tools
RUN go build -trimpath -ldflags="-s -w" -o /out/migrate ./cmd/migrate \
 && go build -trimpath -ldflags="-s -w" -o /out/keyctl ./cmd/keyctl

# --- Runtime -------------------------------------------------------------------------------------------------------
# Distroless static: no shell, no package manager, no libc; runs as the unprivileged user "nonroot" (uid 65532).
# Run containers with a read-only root filesystem and all capabilities dropped (see deployments/local/docker-compose.yml).
FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab AS runtime
USER 65532:65532

FROM runtime AS api
COPY --from=build-api /out/api /usr/local/bin/api
EXPOSE 8080
# The image has no shell or curl, so the binary probes itself: GET /healthz on the loopback address.
HEALTHCHECK --interval=10s --timeout=5s --start-period=10s --retries=3 CMD ["/usr/local/bin/api", "healthcheck"]
ENTRYPOINT ["/usr/local/bin/api"]

FROM runtime AS tools
COPY --from=build-tools /out/migrate /usr/local/bin/migrate
COPY --from=build-tools /out/keyctl /usr/local/bin/keyctl
# No default command on purpose: say which tool to run, for example `migrate up`.
ENTRYPOINT []
