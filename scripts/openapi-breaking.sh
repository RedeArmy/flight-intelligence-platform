#!/bin/sh
# Fails when the OpenAPI contract changes in a way that breaks existing /v1 clients (ADR-015).
# Compares api/openapi/v1/openapi.yaml with the version on the base branch (default origin/main).
# Skips cleanly when the base branch has no contract yet (first introduction).
set -eu

BASE_REF="${1:-origin/main}"
SPEC="api/openapi/v1/openapi.yaml"
OASDIFF_VERSION="v1.32.1"

if ! git cat-file -e "${BASE_REF}:${SPEC}" 2>/dev/null; then
  echo "No contract on ${BASE_REF} yet; nothing to compare."
  exit 0
fi

tmp="$(mktemp)"
trap 'rm -f "$tmp"' EXIT
git show "${BASE_REF}:${SPEC}" > "$tmp"

echo "Checking ${SPEC} against ${BASE_REF} for breaking changes..."
GOFLAGS= go run "github.com/oasdiff/oasdiff@${OASDIFF_VERSION}" breaking "$tmp" "$SPEC" --fail-on ERR
