# Flight Intelligence Platform: developer and CI entry points (Constitution section 72, ADR-028).
# Recipes are kept shell-agnostic so they work with GNU make on Windows (cmd/PowerShell) and Linux CI.
# Windows users without make can run the same targets with: scripts\dev.ps1 <target>

GOLANGCI_LINT_VERSION := v2.14.0
GOVULNCHECK_VERSION   := v1.8.0
GOSEC_VERSION         := v2.29.0
GITLEAKS_VERSION      := v8.30.1
ACTIONLINT_VERSION    := v1.7.12
OAPI_CODEGEN_VERSION  := v2.8.0

export GOTOOLCHAIN := local
export GOFLAGS     := -mod=readonly
export GOBIN       := $(CURDIR)/bin

ifeq ($(OS),Windows_NT)
EXE := .exe
else
EXE :=
endif

GOLANGCI_LINT := $(GOBIN)/golangci-lint$(EXE)
GOVULNCHECK   := $(GOBIN)/govulncheck$(EXE)
GOSEC         := $(GOBIN)/gosec$(EXE)
GITLEAKS      := $(GOBIN)/gitleaks$(EXE)
ACTIONLINT    := $(GOBIN)/actionlint$(EXE)
OAPI_CODEGEN  := $(GOBIN)/oapi-codegen$(EXE)

.DEFAULT_GOAL := help
COMPOSE := docker compose -f deployments/local/docker-compose.yml

.PHONY: help setup hooks tools fmt vet lint workflows generate openapi run local-secrets db-up db-down test-db test-db-down migrate migration-check test test-race integration coverage coverage-integration arch security vuln sast secrets build ci

help: ## List targets
	@echo Targets: setup hooks tools fmt vet lint workflows generate openapi run local-secrets db-up db-down test-db test-db-down migrate migration-check test test-race integration coverage coverage-integration arch security vuln sast secrets build ci
	@echo Planned (added by later E1 slices): dev restore-drill

setup: tools hooks ## Install pinned tools and enable git hooks

hooks: ## Enable the repository git hooks (blocks direct commits/pushes to main)
	git config core.hooksPath .githooks

tools: $(GOLANGCI_LINT) $(GOVULNCHECK) $(GOSEC) $(GITLEAKS) $(ACTIONLINT) $(OAPI_CODEGEN) ## Install pinned dev tools into ./bin

$(OAPI_CODEGEN):
	go install github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@$(OAPI_CODEGEN_VERSION)

$(ACTIONLINT):
	go install github.com/rhysd/actionlint/cmd/actionlint@$(ACTIONLINT_VERSION)

$(GOLANGCI_LINT):
	go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)

$(GOVULNCHECK):
	go install golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)

$(GOSEC):
	go install github.com/securego/gosec/v2/cmd/gosec@$(GOSEC_VERSION)

# NOTE: the module still declares its path as zricethezav (gitleaks/gitleaks fails go install).
$(GITLEAKS):
	go install github.com/zricethezav/gitleaks/v8@$(GITLEAKS_VERSION)

fmt: $(GOLANGCI_LINT) ## Format code (gofmt + goimports)
	$(GOLANGCI_LINT) fmt

vet: ## go vet
	go vet ./...

lint: $(GOLANGCI_LINT) ## Lint and verify formatting
	$(GOLANGCI_LINT) run ./...

workflows: $(ACTIONLINT) ## Lint GitHub Actions workflows
	$(ACTIONLINT)

generate: $(OAPI_CODEGEN) ## Regenerate the server code from api/openapi/v1/openapi.yaml
	$(OAPI_CODEGEN) -config api/openapi/v1/oapi-codegen.yaml api/openapi/v1/openapi.yaml

openapi: ## Validate the OpenAPI contract and its match with the routes and access policies
	go test -count=1 -run "TestOpenAPI|TestEveryOperation|TestRegisteredRoutes|TestErrorEnvelope|TestEveryDocumented" ./internal/platform/httpserver/

run: ## Run the API locally (reads ./.env when APP_ENV is local or test)
	go run ./cmd/api

local-secrets: ## Generate local dev secrets into ./secrets (git-ignored); never overwrites existing ones
	go run ./scripts/devsecrets

db-up: ## Start the local PostgreSQL (needs make local-secrets first)
	$(COMPOSE) up -d --wait postgres

db-down: ## Stop the local PostgreSQL (keeps its data volume)
	$(COMPOSE) stop postgres

test-db: ## Start the throw-away integration-test PostgreSQL (127.0.0.1:55432) and Redis (127.0.0.1:56379), RAM-backed
	$(COMPOSE) --profile test up -d --wait postgres-test redis-test

test-db-down: ## Remove the throw-away integration-test PostgreSQL and Redis
	$(COMPOSE) --profile test rm -fsv postgres-test redis-test

migrate: ## Apply database migrations to the local PostgreSQL as fip_migrator
	go run ./cmd/migrate up

# Validates the migrations end to end: nothing already applied was edited, the policy tests pass, and against a real
# PostgreSQL every migration applies and reverts, up-down-up rebuilds the same schema, and the schema equals the
# committed snapshot. Needs make test-db (the DSN is explicit, so a missing database fails instead of skipping).
migration-check: export TEST_POSTGRES_DSN ?= postgres://postgres@127.0.0.1:55432/postgres?sslmode=disable
migration-check: ## Validate the migrations (append-only, policy, round trip, schema snapshot)
	go run ./scripts/migrationcheck origin/main
	go test -count=1 ./migrations/
	go test -tags integration -count=1 ./internal/platform/database/migrate/

test: ## Unit tests
	go test -count=1 ./...

test-race: ## Unit tests with the race detector (needs a C toolchain on Windows)
	go test -race -count=1 ./...

# The DSN is set explicitly on purpose: when TEST_POSTGRES_DSN is set the harness FAILS if the database is unreachable
# instead of silently skipping, so a missing test database can never look like a green run.
integration: export TEST_POSTGRES_DSN ?= postgres://postgres@127.0.0.1:55432/postgres?sslmode=disable
integration: export TEST_REDIS_ADDR ?= 127.0.0.1:56379
integration: ## Integration tests against PostgreSQL (run make test-db first, or set TEST_POSTGRES_DSN)
	go test -tags integration -count=1 ./...

coverage: ## Unit tests only, with a coverage profile (quick local check)
	go test -count=1 -covermode=atomic -coverprofile=coverage.out ./...

# What SonarCloud reads. Unit AND integration tests run, because the database code is only exercised against a real
# PostgreSQL. -coverpkg=./... credits a package for code that tests in other packages exercise, and covmerge then writes
# each block once (go test repeats a block once per test binary, and readers may keep only one of the copies).
coverage-integration: export TEST_POSTGRES_DSN ?= postgres://postgres@127.0.0.1:55432/postgres?sslmode=disable
coverage-integration: export TEST_REDIS_ADDR ?= 127.0.0.1:56379
integration: export TEST_REDIS_ADDR ?= 127.0.0.1:56379
coverage-integration: ## Unit + integration coverage, merged into coverage.out (needs make test-db)
	go test -tags integration -count=1 -covermode=atomic -coverpkg=./... -coverprofile=coverage.raw ./...
	go run ./scripts/covmerge -in coverage.raw -out coverage.out

arch: ## Architecture rules and docs link check
	go test -count=1 ./internal/archtest/...

vuln: $(GOVULNCHECK) ## Known-vulnerability scan of dependencies and stdlib
	$(GOVULNCHECK) ./...

sast: $(GOSEC) ## Static security analysis
	$(GOSEC) -quiet -exclude-generated ./...

secrets: $(GITLEAKS) ## Scan the git history and working tree for secrets
	$(GITLEAKS) git --no-banner --redact .
	$(GITLEAKS) dir --no-banner --redact .

security: vuln sast secrets ## All security scans

build: ## Compile every package
	go build ./...

ci: vet lint workflows test arch security build ## Everything CI runs, locally
