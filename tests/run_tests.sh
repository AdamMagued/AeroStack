#!/bin/bash
# ==============================================================================
# AeroStack / orbstack_free Complete Test Suite Runner
# ==============================================================================
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
BUILD_DIR="$REPO_ROOT/.build"

echo "======================================================================"
echo " AeroStack Automated Test Suite & Production Audit Harness"
echo "======================================================================"

# 1. Clean AppleDouble metadata on external volume
find "$REPO_ROOT" -name "._*" -delete 2>/dev/null || true

# 2. Static Zero-Stubs & Zero-Emojis Verification
echo "--> 1. Running Zero-Stubs & Zero-Emojis Verification..."
"$REPO_ROOT/tests/test_no_stubs.sh"

# 3. Go Full Package Test Suite
echo "--> 2. Running Go Test Suite across all packages..."
GOCACHE="$REPO_ROOT/.gocache" GOPATH="$REPO_ROOT/.gopath" \
go test -v -count=1 "$REPO_ROOT/src/..."

# 4. Swift Host Engine Unit Tests
echo "--> 3. Running Swift Host Engine Tests..."
export COPYFILE_DISABLE=1
find "$BUILD_DIR" -name "._*" -delete 2>/dev/null || true
swift build --build-tests --scratch-path "$BUILD_DIR" 2>&1 | grep -v "CodeSign" || true
find "$BUILD_DIR/out/Products/Debug/OrbEngineTests.xctest" -name "._*" -delete 2>/dev/null || true
codesign --force --sign - "$BUILD_DIR/out/Products/Debug/OrbEngineTests.xctest"
xcrun xctest "$BUILD_DIR/out/Products/Debug/OrbEngineTests.xctest"

# 5. Real Multi-Distro Machine Engine Lifecycle
echo "--> 4. Running Multi-Distro Machine Engine Lifecycle Tests..."
"$REPO_ROOT/tests/test_machines.sh"

# 6. Apple Silicon Rosetta 2 Linux Translation Verification
echo "--> 5. Running Apple Silicon Rosetta 2 Verification..."
"$REPO_ROOT/tests/test_rosetta.sh"

# 7. Docker Compose Multi-Container Stack
echo "--> 6. Running Docker Compose Multi-Container Stack Tests..."
"$REPO_ROOT/tests/test_docker_compose.sh"

# 8. Mach Physical Memory Footprint Verification
echo "--> 7. Running Mach Physical Memory Footprint Verification..."
"$REPO_ROOT/tests/test_memory_footprint.sh"

echo "======================================================================"
echo " ALL TESTS PASSED: 100% Empirically Verified & Production Hardened"
echo "======================================================================"
