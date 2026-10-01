# Windows wrapper for the Makefile targets (ADR-028). Usage: .\scripts\dev.ps1 <target>
# Mirrors the Makefile exactly; keep both in sync. CI uses the Makefile.
param(
    [Parameter(Position = 0)]
    [ValidateSet("help", "setup", "hooks", "tools", "fmt", "vet", "lint", "workflows", "test", "test-race", "integration", "coverage", "arch", "vuln", "sast", "secrets", "security", "build", "ci")]
    [string]$Target = "help"
)

$ErrorActionPreference = "Stop"
Set-Location (Split-Path -Parent $PSScriptRoot)

$GolangciLintVersion = "v2.14.0"
$GovulncheckVersion = "v1.8.0"
$GosecVersion = "v2.29.0"
$GitleaksVersion = "v8.30.1"
$ActionlintVersion = "v1.7.12"

$env:GOTOOLCHAIN = "local"
$env:GOFLAGS = "-mod=readonly"
$env:GOBIN = Join-Path (Get-Location) "bin"

function Invoke-Native {
    param([string]$Exe, [string[]]$Arguments)
    & $Exe @Arguments
    if ($LASTEXITCODE -ne 0) { throw "$Exe $($Arguments -join ' ') failed with exit code $LASTEXITCODE" }
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
}

function Bin([string]$Name) { Join-Path $env:GOBIN "$Name.exe" }

switch ($Target) {
    "workflows" { Tools; Invoke-Native (Bin "actionlint") @() }
    "help" { "Targets: setup hooks tools fmt vet lint workflows test test-race integration coverage arch vuln sast secrets security build ci" }
    "hooks" { Invoke-Native git @("config", "core.hooksPath", ".githooks") }
    "tools" { Tools }
    "setup" { Tools; Invoke-Native git @("config", "core.hooksPath", ".githooks") }
    "fmt" { Tools; Invoke-Native (Bin "golangci-lint") @("fmt") }
    "vet" { Invoke-Native go @("vet", "./...") }
    "lint" { Tools; Invoke-Native (Bin "golangci-lint") @("run", "./...") }
    "test" { Invoke-Native go @("test", "-count=1", "./...") }
    "test-race" { Invoke-Native go @("test", "-race", "-count=1", "./...") }
    "integration" { Invoke-Native go @("test", "-tags", "integration", "-count=1", "./...") }
    "coverage" { Invoke-Native go @("test", "-count=1", "-covermode=atomic", "-coverprofile=coverage.out", "./...") }
    "arch" { Invoke-Native go @("test", "-count=1", "./internal/archtest/...") }
    "vuln" { Tools; Invoke-Native (Bin "govulncheck") @("./...") }
    "sast" { Tools; Invoke-Native (Bin "gosec") @("-quiet", "./...") }
    "secrets" {
        Tools
        Invoke-Native (Bin "gitleaks") @("git", "--no-banner", "--redact", ".")
        Invoke-Native (Bin "gitleaks") @("dir", "--no-banner", "--redact", ".")
    }
    "security" {
        Tools
        Invoke-Native (Bin "govulncheck") @("./...")
        Invoke-Native (Bin "gosec") @("-quiet", "./...")
        Invoke-Native (Bin "gitleaks") @("git", "--no-banner", "--redact", ".")
        Invoke-Native (Bin "gitleaks") @("dir", "--no-banner", "--redact", ".")
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
        Invoke-Native (Bin "gosec") @("-quiet", "./...")
        Invoke-Native (Bin "gitleaks") @("git", "--no-banner", "--redact", ".")
        Invoke-Native (Bin "gitleaks") @("dir", "--no-banner", "--redact", ".")
        Invoke-Native go @("build", "./...")
    }
}
