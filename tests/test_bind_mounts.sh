#!/bin/bash
# ==============================================================================
# AeroStack Test: Host Bind Mounts (-v) Translation Verification
# ==============================================================================
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

echo "=== [TEST] Host Bind Mounts Path Translation ==="

# 1. Clean AppleDouble files
find "$REPO_ROOT" -name "._*" -delete 2>/dev/null || true

# 2. Run bridge package unit tests with race detection
echo "--> 1. Running path translator unit tests (Binds & Mounts arrays)..."
GOCACHE="$REPO_ROOT/.gocache" GOPATH="$REPO_ROOT/.gopath" \
go test -v -race "$REPO_ROOT/src/host/bridge/..."

echo "=== [TEST] Host Bind Mounts Verification PASSED 100% ==="
