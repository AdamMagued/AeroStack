#!/bin/bash
# ==============================================================================
# AeroStack Test: Container Port Forwarding (-p) Verification
# ==============================================================================
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

echo "=== [TEST] Container Port Forwarding (-p) Verification ==="

# 1. Clean AppleDouble files
find "$REPO_ROOT" -name "._*" -delete 2>/dev/null || true

# 2. Run Network & DockerPortWatcher Unit Tests
echo "--> 1. Running DockerPortWatcher and PortForwarder unit tests..."
GOCACHE="$REPO_ROOT/.gocache" GOPATH="$REPO_ROOT/.gopath" \
go test -v "$REPO_ROOT/src/host/network/..."

echo "=== [TEST] Container Port Forwarding Verification PASSED 100% ==="
