# Windows wrapper for the Makefile targets (ADR-028). Usage: .\scripts\dev.ps1 <target>
# Mirrors the Makefile exactly; keep both in sync. CI uses the Makefile.
param(
    [Parameter(Position = 0)]
    [ValidateSet("help", "setup", "hooks", "tools", "fmt", "vet", "lint", "workflows", "generate", "openapi", "run", "local-secrets", "dev", "dev-down", "stack", "images", "dockerfile-lint", "compose-check", "observability-check", "image-scan", "sbom", "keyctl", "restore-drill", "db-up", "db-down", "test-db", "test-db-down", "migrate", "migration-check", "test", "test-race", "test-race-docker", "integration", "coverage", "coverage-integration", "arch", "vuln", "sast", "secrets", "security", "build", "ci")]
    [string]$Target = "help",
    # Extra arguments for targets that take some, for example: .\scripts\dev.ps1 keyctl key list
    [Parameter(ValueFromRemainingArguments = $true)]
    [string[]]$Rest = @()
)

$ErrorActionPreference = "Stop"
Set-Location (Split-Path -Parent $PSScriptRoot)

$GolangciLintVersion = "v2.14.0"
$GovulncheckVersion = "v1.8.0"
$GosecVersion = "v2.29.0"
$GitleaksVersion = "v8.30.1"
$ActionlintVersion = "v1.7.12"
$OapiCodegenVersion = "v2.8.0"
# Linux image with a C compiler, for the race detector (keep in sync with RACE_IMAGE in the Makefile).
# Container tooling, pinned like in the Makefile (keep in sync with TRIVY_IMAGE, SYFT_IMAGE, HADOLINT_IMAGE there).
$TrivyImage = "aquasec/trivy:0.75.0@sha256:af6acf9a6b85dfe389a1941505c0ce9efef52a4719635e1a962f022a3d855daa"
$SyftImage = "anchore/syft:v1.54.0@sha256:0356562f495d432056237fbea5cbc2d4839c9c75cd500784a66de2e7cc95ca7c"
$PrometheusImage = "prom/prometheus:v3.15.0@sha256:efd719c99d83b060d9daefdcf00360461adf279f45ef5391f8d111892118753e"
$OtelcolImage = "otel/opentelemetry-collector-contrib:0.161.0@sha256:fd328de2552466ad78385e1b1289c3f2402b1c45f265b252aab1955b42845ac1"
$HadolintImage = "hadolint/hadolint:v2.15.1-debian@sha256:9a3944b7fddcb947d1ffd90829ac1a6e5c30479223358f249d8b96c7d0019e27"
$RaceImage = "golang:1.27.1@sha256:e0174e51e81218523251d85d248a90d24c3d5e81543b4f07a5d66229397db190"

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
    "help" { "Targets: setup hooks tools fmt vet lint workflows generate openapi run local-secrets dev dev-down stack images dockerfile-lint compose-check observability-check image-scan sbom keyctl restore-drill db-up db-down test-db test-db-down migrate migration-check test test-race test-race-docker integration coverage coverage-integration arch vuln sast secrets security build ci" }
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
    "dev" { Invoke-Native go @("run", "./scripts/devsecrets"); Invoke-Native docker (Compose @("up", "-d", "--wait")) }
    "dev-down" { Invoke-Native docker (Compose @("--profile", "app", "down")) }
    "stack" { Invoke-Native go @("run", "./scripts/devsecrets"); Invoke-Native docker (Compose @("--profile", "app", "up", "-d", "--build", "--wait")) }
    "images" {
        Invoke-Native docker @("build", "--target", "api", "-t", "fip-api:local", ".")
        Invoke-Native docker @("build", "--target", "worker", "-t", "fip-worker:local", ".")
        Invoke-Native docker @("build", "--target", "tools", "-t", "fip-tools:local", ".")
    }
    "dockerfile-lint" {
        Get-Content -Raw Dockerfile | & docker run --rm -i $HadolintImage hadolint --failure-threshold warning -
        if ($LASTEXITCODE -ne 0) { throw "hadolint failed with exit code $LASTEXITCODE" }
    }
    "compose-check" { Invoke-Native docker (Compose @("--profile", "app", "--profile", "tools", "--profile", "test", "config", "-q")) }
    "observability-check" {
        $local = Join-Path (Get-Location) "deployments\local"
        Invoke-Native docker @("run", "--rm", "--entrypoint", "/bin/promtool", "-v", "$local\prometheus-rules:/etc/prometheus/rules:ro", "-v", "$local\prometheus.yml:/etc/prometheus/prometheus.yml:ro", $PrometheusImage, "check", "config", "/etc/prometheus/prometheus.yml")
        Invoke-Native docker @("run", "--rm", "--entrypoint", "/bin/promtool", "-v", "$local`:/d:ro", $PrometheusImage, "test", "rules", "/d/prometheus-tests/slo_test.yml")
        Invoke-Native docker @("run", "--rm", "-v", "$local\otel-collector.yaml:/etc/otelcol/config.yaml:ro", $OtelcolImage, "validate", "--config", "/etc/otelcol/config.yaml")
    }
    "image-scan" {
        & $PSCommandPath images
        $dist = Join-Path (Get-Location) "dist"
        foreach ($n in @("api", "worker", "tools")) {
            Invoke-Native docker @("save", "fip-$n`:local", "-o", "dist/fip-$n.tar")
            Invoke-Native docker @("run", "--rm", "-v", "$dist`:/in:ro", "-v", "fip-trivy-cache:/root/.cache/trivy", $TrivyImage, "image", "--input", "/in/fip-$n.tar", "--scanners", "vuln", "--severity", "HIGH,CRITICAL", "--ignore-unfixed", "--exit-code", "1", "--no-progress")
        }
    }
    "sbom" {
        & $PSCommandPath images
        $dist = Join-Path (Get-Location) "dist"
        foreach ($n in @("api", "worker", "tools")) {
            Invoke-Native docker @("save", "fip-$n`:local", "-o", "dist/fip-$n.tar")
            Invoke-Native docker @("run", "--rm", "-v", "$dist`:/work", $SyftImage, "scan", "docker-archive:/work/fip-$n.tar", "-o", "spdx-json=/work/fip-$n.spdx.json")
        }
    }
    "keyctl" { Invoke-Native docker (Compose (@("--profile", "tools", "run", "--rm", "keyctl") + $Rest)) }
    "restore-drill" { Invoke-Native go @("run", "./scripts/restoredrill") }
    "db-up" { Invoke-Native docker (Compose @("up", "-d", "--wait", "postgres")) }
    "db-down" { Invoke-Native docker (Compose @("stop", "postgres")) }
    "test-db" { Invoke-Native docker (Compose @("--profile", "test", "up", "-d", "--wait", "postgres-test", "redis-test")) }
    "test-db-down" { Invoke-Native docker (Compose @("--profile", "test", "rm", "-fsv", "postgres-test", "redis-test")) }
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
    "test-race-docker" {
        # Same run as CI's test-race job, in a Linux container: no C compiler needed on this machine.
        Invoke-Native docker @("run", "--rm", "-v", "$((Get-Location).Path):/src:ro", "-v", "fip-gomod:/go/pkg/mod", "-v", "fip-gobuild:/root/.cache/go-build", "-w", "/src", "-e", "GOTOOLCHAIN=local", "-e", "GOFLAGS=-mod=readonly", $RaceImage, "go", "test", "-race", "-count=1", "./...")
    }
    "integration" {
        # Explicit DSN on purpose: an unreachable test database must fail the run, never skip silently.
        if (-not $env:TEST_POSTGRES_DSN) { $env:TEST_POSTGRES_DSN = "postgres://postgres@127.0.0.1:55432/postgres?sslmode=disable" }
        if (-not $env:TEST_REDIS_ADDR) { $env:TEST_REDIS_ADDR = "127.0.0.1:56379" }
        Invoke-Native go @("test", "-tags", "integration", "-count=1", "./...")
    }
    "coverage" { Invoke-Native go @("test", "-count=1", "-covermode=atomic", "-coverprofile=coverage.out", "./...") }
    "coverage-integration" {
        # Explicit DSN on purpose: an unreachable test database must fail the run, never skip silently.
        if (-not $env:TEST_POSTGRES_DSN) { $env:TEST_POSTGRES_DSN = "postgres://postgres@127.0.0.1:55432/postgres?sslmode=disable" }
        if (-not $env:TEST_REDIS_ADDR) { $env:TEST_REDIS_ADDR = "127.0.0.1:56379" }
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
