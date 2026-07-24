#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
go_cache="${GOCACHE:-${TMPDIR:-/tmp}/securebrain-quality-cache}"

cd "$repo_root"
GOCACHE="$go_cache" go test ./...
GOCACHE="$go_cache" go vet ./...
./scripts/check-architecture.sh
