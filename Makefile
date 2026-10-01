# Flight Intelligence Platform: developer and CI entry points (Constitution section 72, ADR-028).
# Recipes are kept shell-agnostic so they work with GNU make on Windows (cmd/PowerShell) and Linux CI.
# Windows users without make can run the same targets with: scripts\dev.ps1 <target>

GOLANGCI_LINT_VERSION := v2.14.0
GOVULNCHECK_VERSION   := v1.8.0
GOSEC_VERSION         := v2.29.0
GITLEAKS_VERSION      := v8.30.1
ACTIONLINT_VERSION    := v1.7.12

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

.DEFAULT_GOAL := help
.PHONY: help setup hooks tools fmt vet lint workflows test test-race integration coverage arch security vuln sast secrets build ci

help: ## List targets
	@echo Targets: setup hooks tools fmt vet lint workflows test test-race integration coverage arch security vuln sast secrets build ci
	@echo Planned (added by later E1 slices): dev migrate generate openapi restore-drill

setup: tools hooks ## Install pinned tools and enable git hooks

hooks: ## Enable the repository git hooks (blocks direct commits/pushes to main)
	git config core.hooksPath .githooks

tools: $(GOLANGCI_LINT) $(GOVULNCHECK) $(GOSEC) $(GITLEAKS) $(ACTIONLINT) ## Install pinned dev tools into ./bin

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

test: ## Unit tests
	go test -count=1 ./...

test-race: ## Unit tests with the race detector (needs a C toolchain on Windows)
	go test -race -count=1 ./...

integration: ## Integration tests (need PostgreSQL and Redis; none exist yet)
	go test -tags integration -count=1 ./...

coverage: ## Unit tests with a coverage profile (used by SonarCloud)
	go test -count=1 -covermode=atomic -coverprofile=coverage.out ./...

arch: ## Architecture rules and docs link check
	go test -count=1 ./internal/archtest/...

vuln: $(GOVULNCHECK) ## Known-vulnerability scan of dependencies and stdlib
	$(GOVULNCHECK) ./...

sast: $(GOSEC) ## Static security analysis
	$(GOSEC) -quiet ./...

secrets: $(GITLEAKS) ## Scan the git history and working tree for secrets
	$(GITLEAKS) git --no-banner --redact .
	$(GITLEAKS) dir --no-banner --redact .

security: vuln sast secrets ## All security scans

build: ## Compile every package
	go build ./...

ci: vet lint workflows test arch security build ## Everything CI runs, locally
