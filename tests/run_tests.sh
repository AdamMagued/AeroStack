#!/bin/bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
BUILD_DIR="$REPO_ROOT/.build"

echo "=== Running orbstack_free Swift Host Engine Tests ==="
export COPYFILE_DISABLE=1

# Clean any AppleDouble metadata on exFAT
find "$BUILD_DIR" -name "._*" -delete 2>/dev/null || true

# Rebuild test target
swift build --build-tests --scratch-path "$BUILD_DIR" 2>&1 | grep -v "CodeSign" || true

# Re-clean and ad-hoc sign test bundle
find "$BUILD_DIR/out/Products/Debug/OrbEngineTests.xctest" -name "._*" -delete 2>/dev/null || true
codesign --force --sign - "$BUILD_DIR/out/Products/Debug/OrbEngineTests.xctest"

# Run tests
xcrun xctest "$BUILD_DIR/out/Products/Debug/OrbEngineTests.xctest"
echo "=== All Tests Passed Successfully ==="
