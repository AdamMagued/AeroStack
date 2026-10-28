#!/bin/bash
# ==============================================================================
# AeroStack Test: Real Multi-Distro Machine Engine Lifecycle
# ==============================================================================
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
BUILD_DIR="$REPO_ROOT/build"
ORBCTL="$BUILD_DIR/orbctl"

mkdir -p "$BUILD_DIR"

echo "=== [TEST] Building orbctl binary ==="
go build -o "$ORBCTL" "$REPO_ROOT/src/host/cli/"

echo "=== [TEST] 1. Creating Real Ubuntu Machine (test-ci-ubuntu) ==="
"$ORBCTL" create ubuntu:24.04 test-ci-ubuntu

DATA_DIR="${HOME}/.aerostack/data/machines"
if [ ! -d "$DATA_DIR/test-ci-ubuntu" ]; then
    echo "FAIL: Machine directory was not created at $DATA_DIR/test-ci-ubuntu"
    exit 1
fi

if [ ! -f "$DATA_DIR/test-ci-ubuntu/rootfs.img" ]; then
    echo "FAIL: Machine rootfs.img missing"
    exit 1
fi

if [ ! -f "$DATA_DIR/test-ci-ubuntu/metadata.json" ]; then
    echo "FAIL: Machine metadata.json missing"
    exit 1
fi
echo "PASSED: Verified disk image and metadata persistence"

echo "=== [TEST] 2. Starting Machine ==="
"$ORBCTL" start test-ci-ubuntu
STATUS=$("$ORBCTL" list -j | python3 -c "
import sys, json
data = json.load(sys.stdin)
for m in data:
    if m['name'] == 'test-ci-ubuntu':
        print(m['state'])
")
if [ "$STATUS" != "RUNNING" ]; then
    echo "FAIL: Expected RUNNING status, got $STATUS"
    exit 1
fi
echo "PASSED: Machine transitioned to RUNNING"

echo "=== [TEST] 3. Cloning Machine via CoW clonefile (test-ci-ubuntu -> test-ci-ubuntu-clone) ==="
"$ORBCTL" clone test-ci-ubuntu test-ci-ubuntu-clone
if [ ! -f "$DATA_DIR/test-ci-ubuntu-clone/rootfs.img" ]; then
    echo "FAIL: Cloned disk missing"
    exit 1
fi
echo "PASSED: Cloned machine disk and metadata created"

echo "=== [TEST] 4. Listing Machines ==="
LIST_OUT=$("$ORBCTL" list)
echo "$LIST_OUT"
if ! echo "$LIST_OUT" | grep -q "test-ci-ubuntu"; then
    echo "FAIL: test-ci-ubuntu not in list output"
    exit 1
fi
if ! echo "$LIST_OUT" | grep -q "test-ci-ubuntu-clone"; then
    echo "FAIL: test-ci-ubuntu-clone not in list output"
    exit 1
fi
echo "PASSED: Both machines listed accurately"

echo "=== [TEST] 5. Stopping and Deleting Machines ==="
"$ORBCTL" stop test-ci-ubuntu
"$ORBCTL" delete test-ci-ubuntu
"$ORBCTL" delete test-ci-ubuntu-clone

if [ -d "$DATA_DIR/test-ci-ubuntu" ] || [ -d "$DATA_DIR/test-ci-ubuntu-clone" ]; then
    echo "FAIL: Deleted machine directories still exist on disk"
    exit 1
fi
echo "PASSED: Cleaned up machine directories completely"

echo "=== [TEST] Real Multi-Distro Machine Engine Lifecycle PASSED 100% ==="
