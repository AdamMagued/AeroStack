#!/bin/bash
# ==============================================================================
# AeroStack Test: Host vsock Exec Bridge Verification
# ==============================================================================
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
BUILD_DIR="$REPO_ROOT/.build"

echo "=== [TEST] Host vsock Exec Bridge Verification ==="

# 1. Clean AppleDouble files
find "$REPO_ROOT" -name "._*" -delete 2>/dev/null || true

# 2. Run Swift Engine VsockRouter Exec Bridge Unit Test
echo "--> 1. Running Swift VsockRouter Exec Bridge Unit Test..."
export COPYFILE_DISABLE=1
find "$BUILD_DIR" -name "._*" -delete 2>/dev/null || true
swift build --build-tests --scratch-path "$BUILD_DIR" 2>&1 | grep -v "CodeSign" || true
find "$BUILD_DIR/out/Products/Debug/OrbEngineTests.xctest" -name "._*" -delete 2>/dev/null || true
codesign --force --sign - "$BUILD_DIR/out/Products/Debug/OrbEngineTests.xctest"
xcrun xctest -XCTest OrbEngineTests/testVsockRouterExecBridge "$BUILD_DIR/out/Products/Debug/OrbEngineTests.xctest"

# 3. Run Go Exec Protocol Verification Test
echo "--> 2. Running Go Remote Exec Client Protocol Tests..."
GOCACHE="$REPO_ROOT/.gocache" GOPATH="$REPO_ROOT/.gopath" \
go test -v -run TestCLI_Run "$REPO_ROOT/src/host/cli/..."

echo "=== [TEST] Host vsock Exec Bridge Verification PASSED 100% ==="
