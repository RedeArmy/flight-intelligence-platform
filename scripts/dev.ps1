# Windows wrapper for the Makefile targets (ADR-028). Usage: .\scripts\dev.ps1 <target>
# Mirrors the Makefile exactly; keep both in sync. CI uses the Makefile.
param(
    [Parameter(Position = 0)]
    [ValidateSet("help", "setup", "hooks", "tools", "fmt", "vet", "lint", "workflows", "generate", "openapi", "run", "local-secrets", "db-up", "db-down", "test-db", "test-db-down", "migrate", "migration-check", "test", "test-race", "integration", "coverage", "coverage-integration", "arch", "vuln", "sast", "secrets", "security", "build", "ci")]
    [string]$Target = "help"
)

$ErrorActionPreference = "Stop"
Set-Location (Split-Path -Parent $PSScriptRoot)

$GolangciLintVersion = "v2.14.0"
$GovulncheckVersion = "v1.8.0"
$GosecVersion = "v2.29.0"
$GitleaksVersion = "v8.30.1"
$ActionlintVersion = "v1.7.12"
$OapiCodegenVersion = "v2.8.0"

$env:GOTOOLCHAIN = "local"
$env:GOFLAGS = "-mod=readonly"
$env:GOBIN = Join-Path (Get-Location) "bin"

function Invoke-Native {
    param([string]$Exe, [string[]]$Arguments)
    # Tools such as docker and go print progress on stderr; only the exit code decides success.
    $previous = $ErrorActionPreference
    $ErrorActionPreference = "Continue"
    try {
        & $Exe @Arguments
        $code = $LASTEXITCODE
    } finally {
        $ErrorActionPreference = $previous
    }
    if ($code -ne 0) { throw "$Exe $($Arguments -join ' ') failed with exit code $code" }
}

function Install-Tool {
    param([string]$Name, [string]$Package)
    if (-not (Test-Path (Join-Path $env:GOBIN "$Name.exe"))) {
        Invoke-Native go @("install", $Package)
    }
}

function Tools {
    Install-Tool "golangci-lint" "github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$GolangciLintVersion"
    Install-Tool "govulncheck" "golang.org/x/vuln/cmd/govulncheck@$GovulncheckVersion"
    Install-Tool "gosec" "github.com/securego/gosec/v2/cmd/gosec@$GosecVersion"
    Install-Tool "gitleaks" "github.com/zricethezav/gitleaks/v8@$GitleaksVersion"
    Install-Tool "actionlint" "github.com/rhysd/actionlint/cmd/actionlint@$ActionlintVersion"
    Install-Tool "oapi-codegen" "github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@$OapiCodegenVersion"
}

function Bin([string]$Name) { Join-Path $env:GOBIN "$Name.exe" }

function Compose([string[]]$Arguments) {
    @("compose", "-f", "deployments/local/docker-compose.yml") + $Arguments
}

function Scan-Secrets {
    Invoke-Native (Bin "gitleaks") @("git", "--no-banner", "--redact", ".")
    Invoke-Native (Bin "gitleaks") @("dir", "--no-banner", "--redact", ".")
}

switch ($Target) {
    "help" { "Targets: setup hooks tools fmt vet lint workflows generate openapi run local-secrets db-up db-down test-db test-db-down migrate migration-check test test-race integration coverage coverage-integration arch vuln sast secrets security build ci" }
    "hooks" { Invoke-Native git @("config", "core.hooksPath", ".githooks") }
    "tools" { Tools }
    "setup" { Tools; Invoke-Native git @("config", "core.hooksPath", ".githooks") }
    "fmt" { Tools; Invoke-Native (Bin "golangci-lint") @("fmt") }
    "vet" { Invoke-Native go @("vet", "./...") }
    "lint" { Tools; Invoke-Native (Bin "golangci-lint") @("run", "./...") }
    "workflows" { Tools; Invoke-Native (Bin "actionlint") @() }
    "generate" { Tools; Invoke-Native (Bin "oapi-codegen") @("-config", "api/openapi/v1/oapi-codegen.yaml", "api/openapi/v1/openapi.yaml") }
    "openapi" {
        Invoke-Native go @("test", "-count=1", "-run", "TestOpenAPI|TestEveryOperation|TestRegisteredRoutes|TestErrorEnvelope|TestEveryDocumented", "./internal/platform/httpserver/")
    }
    "run" { Invoke-Native go @("run", "./cmd/api") }
    "local-secrets" { Invoke-Native go @("run", "./scripts/devsecrets") }
    "db-up" { Invoke-Native docker (Compose @("up", "-d", "--wait", "postgres")) }
    "db-down" { Invoke-Native docker (Compose @("stop", "postgres")) }
    "test-db" { Invoke-Native docker (Compose @("--profile", "test", "up", "-d", "--wait", "postgres-test")) }
    "test-db-down" { Invoke-Native docker (Compose @("--profile", "test", "rm", "-fsv", "postgres-test")) }
    "migrate" { Invoke-Native go @("run", "./cmd/migrate", "up") }
    "migration-check" {
        # Explicit DSN on purpose: an unreachable test database must fail the run, never skip silently.
        if (-not $env:TEST_POSTGRES_DSN) { $env:TEST_POSTGRES_DSN = "postgres://postgres@127.0.0.1:55432/postgres?sslmode=disable" }
        Invoke-Native go @("run", "./scripts/migrationcheck", "origin/main")
        Invoke-Native go @("test", "-count=1", "./migrations/")
        Invoke-Native go @("test", "-tags", "integration", "-count=1", "./internal/platform/database/migrate/")
    }
    "test" { Invoke-Native go @("test", "-count=1", "./...") }
    "test-race" { Invoke-Native go @("test", "-race", "-count=1", "./...") }
    "integration" {
        # Explicit DSN on purpose: an unreachable test database must fail the run, never skip silently.
        if (-not $env:TEST_POSTGRES_DSN) { $env:TEST_POSTGRES_DSN = "postgres://postgres@127.0.0.1:55432/postgres?sslmode=disable" }
        Invoke-Native go @("test", "-tags", "integration", "-count=1", "./...")
    }
    "coverage" { Invoke-Native go @("test", "-count=1", "-covermode=atomic", "-coverprofile=coverage.out", "./...") }
    "coverage-integration" {
        # Explicit DSN on purpose: an unreachable test database must fail the run, never skip silently.
        if (-not $env:TEST_POSTGRES_DSN) { $env:TEST_POSTGRES_DSN = "postgres://postgres@127.0.0.1:55432/postgres?sslmode=disable" }
        Invoke-Native go @("test", "-tags", "integration", "-count=1", "-covermode=atomic", "-coverpkg=./...", "-coverprofile=coverage.raw", "./...")
        Invoke-Native go @("run", "./scripts/covmerge", "-in", "coverage.raw", "-out", "coverage.out")
    }
    "arch" { Invoke-Native go @("test", "-count=1", "./internal/archtest/...") }
    "vuln" { Tools; Invoke-Native (Bin "govulncheck") @("./...") }
    "sast" { Tools; Invoke-Native (Bin "gosec") @("-quiet", "-exclude-generated", "./...") }
    "secrets" { Tools; Scan-Secrets }
    "security" {
        Tools
        Invoke-Native (Bin "govulncheck") @("./...")
        Invoke-Native (Bin "gosec") @("-quiet", "-exclude-generated", "./...")
        Scan-Secrets
    }
    "build" { Invoke-Native go @("build", "./...") }
    "ci" {
        Tools
        Invoke-Native go @("vet", "./...")
        Invoke-Native (Bin "golangci-lint") @("run", "./...")
        Invoke-Native (Bin "actionlint") @()
        Invoke-Native go @("test", "-count=1", "./...")
        Invoke-Native go @("test", "-count=1", "./internal/archtest/...")
        Invoke-Native (Bin "govulncheck") @("./...")
        Invoke-Native (Bin "gosec") @("-quiet", "-exclude-generated", "./...")
        Scan-Secrets
        Invoke-Native go @("build", "./...")
    }
}
