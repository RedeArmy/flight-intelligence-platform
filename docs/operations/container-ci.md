# Container checks in CI

What the `images` job checks, how to run the same checks locally, and what to do when one fails (ADR-029, ADR-034).
The checks are `make` targets, so a developer runs exactly what CI runs. The tools are container images pinned by tag and
digest in the `Makefile` (`TRIVY_IMAGE`, `SYFT_IMAGE`, `HADOLINT_IMAGE`) and mirrored in `scripts/dev.ps1`.

| Target | What it does | Fails when |
|--------|--------------|------------|
| `make dockerfile-lint` | Lints the `Dockerfile` with hadolint | any finding at warning level or above |
| `make compose-check` | Validates `deployments/local/docker-compose.yml` with every profile enabled | the file is invalid |
| `make observability-check` | Validates the Prometheus and collector configuration and runs the unit tests of the SLO alert rules (promtool) | a rule, a configuration or a test is wrong |
| `make image-scan` | Builds the images, saves each one to `dist/`, scans it with Trivy | an operating-system package or a Go module has a **HIGH or CRITICAL vulnerability that has a fix** |
| `make sbom` | Writes an SPDX 2.3 software bill of materials per image to `dist/` with Syft | the tool fails; it checks nothing else |

On Windows without `make`, use `.\scripts\dev.ps1 <target>`. Docker is required. The first `image-scan` downloads the Trivy
vulnerability database and builds three images, so it takes several minutes; later runs reuse the `fip-trivy-cache`
Docker volume. `dist/` is git-ignored (only `dist/.gitkeep` is tracked).

## Repository settings these jobs need
These are settings of the GitHub repository, not files; the workflow cannot change them, and a job fails if one is missing.

| Setting (Settings, Code security) | Needed by | Symptom when missing |
|-----------------------------------|-----------|----------------------|
| **Dependency graph** enabled | `dependency-review` | `Dependency review is not supported on this repository. Please ensure that Dependency graph is enabled` |
| **CodeQL default setup** turned off | `codeql` | results from the workflow are rejected: "advanced configuration cannot be processed when default setup is enabled" |

The dependency graph is free for public repositories. Enable it once; it also feeds Dependabot alerts.

## What the scan covers
- The base image's operating-system packages (Debian, from the distroless image).
- The Go modules compiled into each binary, read from the binary itself. This finds vulnerable dependencies that
  `govulncheck` does not report because the vulnerable function is never called: Trivy reports a module version, not a call
  path. That is intended; a fixable HIGH in a dependency should be updated even when we do not call it today.
- Vulnerabilities **without a fix** are ignored (`--ignore-unfixed`): nothing can be done about them yet. They appear when a
  fix is published.

## When the scan fails
1. Read the table: it names the library, the installed version and the fixed version.
2. **A Go module:** update it (`go get module@fixed`, `go mod tidy`), run `make ci` and `make integration`, and rescan.
   Indirect modules are updated the same way (for example `google.golang.org/grpc`, which the OpenTelemetry exporters pull in).
3. **A base image package:** the two base images are referenced by tag and digest in the `Dockerfile`; Dependabot proposes
   updates for them (the `docker` ecosystem). If a fix is already in a newer image, merge that update or update it by hand
   (pull the new tag, copy its digest into the `FROM` line) and rescan.
4. **No fix or a false positive:** there is no exception file yet, on purpose: an exception is a decision that needs an
   owner, a reason and an expiry. When the first real case appears, add an ignore file (Trivy `--ignorefile`) in the same pull
   request as an ADR note that records those three things.

## The other checks of this slice
- **dependency-review** runs on pull requests only. It blocks a pull request that adds or updates a dependency with a HIGH or
  CRITICAL vulnerability, including GitHub Actions. It needs the **Dependency graph** to be enabled (see above). It is
  skipped on pushes to `main`; `ci-gate` accepts that.
- **codeql** analyses the Go code with the `security-extended` queries and uploads the results to the repository's Security
  tab. It needs `security-events: write`, so it is skipped for pull requests from forks (like `sonar`). It also needs the
  CodeQL *default setup* to be off (see above).
- **Caches:** `setup-go` caches the module download and the build output, keyed on `go.sum`.

## SBOMs
Each `images` run uploads `sbom` as a workflow artifact (three `*.spdx.json` files, kept 30 days). They list the operating-system
packages and Go modules of each image, and are what to read when a new vulnerability is announced and the question is
whether an image contains the affected component.

## Updating the tools
Each tool image is pinned by version tag and digest. To update one: pull the new tag, read the digest
(`docker inspect --format '{{index .RepoDigests 0}}' <image>`), replace tag and digest together in the `Makefile` and in
`scripts/dev.ps1`, and run the target. A newer Trivy also brings a newer database format, so run `make image-scan` once after
updating it.

## Not done yet
Images are built and scanned but not published or signed: there is no registry stage (D1, ADR-025). Signing and provenance
arrive with the deployment decision.
