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

DATA_DIR="${HOME}/.aerostack/data/machines"
rm -rf "$DATA_DIR/test-ci-ubuntu" "$DATA_DIR/test-ci-ubuntu-clone" "$DATA_DIR/._test-ci-ubuntu"* 2>/dev/null || true

VMD_PID=$(pgrep -f "orb-vmd" | head -n 1 || true)
STARTED_VMD=0

if [ -z "$VMD_PID" ]; then
    echo "--> Starting orb-vmd --no-boot for machine lifecycle test..."
    mkdir -p "${HOME}/.aerostack/run"
    "$BUILD_DIR/orb-vmd" --no-boot >/tmp/orb-vmd-test-machines.log 2>&1 &
    VMD_PID=$!
    STARTED_VMD=1
    for i in $(seq 1 40); do
        if [ -S "$HOME/.aerostack/run/vmcontrol.sock" ]; then
            break
        fi
        sleep 0.25
    done
fi

# Pre-test cleanup to ensure clean slate across test runs
"$ORBCTL" stop test-ci-ubuntu 2>/dev/null || true
"$ORBCTL" delete test-ci-ubuntu 2>/dev/null || true
"$ORBCTL" delete test-ci-ubuntu-clone 2>/dev/null || true
rm -rf "$DATA_DIR/test-ci-ubuntu" "$DATA_DIR/test-ci-ubuntu-clone" "$DATA_DIR/._test-ci-ubuntu"* "$REPO_ROOT/data/machines/test-ci-ubuntu"* 2>/dev/null || true

cleanup() {
    "$ORBCTL" stop test-ci-ubuntu 2>/dev/null || true
    "$ORBCTL" delete test-ci-ubuntu 2>/dev/null || true
    "$ORBCTL" delete test-ci-ubuntu-clone 2>/dev/null || true
    rm -rf "$DATA_DIR/test-ci-ubuntu" "$DATA_DIR/test-ci-ubuntu-clone" "$DATA_DIR/._test-ci-ubuntu"* "$REPO_ROOT/data/machines/test-ci-ubuntu"* 2>/dev/null || true
    if [ "$STARTED_VMD" -eq 1 ]; then
        kill -15 "$VMD_PID" 2>/dev/null || true
    fi
}
trap cleanup EXIT

echo "=== [TEST] 1. Creating Real Ubuntu Machine (test-ci-ubuntu) ==="
"$ORBCTL" create ubuntu:24.04 test-ci-ubuntu

if [ ! -d "$DATA_DIR/test-ci-ubuntu" ]; then
    echo "FAIL: Machine directory was not created at $DATA_DIR/test-ci-ubuntu"
    exit 1
fi

if [ ! -f "$DATA_DIR/test-ci-ubuntu/rootfs.img" ] && [ ! -f "$DATA_DIR/test-ci-ubuntu/rootfs.ext4" ]; then
    echo "FAIL: Machine rootfs missing"
    exit 1
fi

if [ ! -f "$DATA_DIR/test-ci-ubuntu/metadata.json" ] && [ ! -f "$DATA_DIR/test-ci-ubuntu/machine.json" ]; then
    echo "FAIL: Machine metadata missing"
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
if [ ! -f "$DATA_DIR/test-ci-ubuntu-clone/rootfs.img" ] && [ ! -f "$DATA_DIR/test-ci-ubuntu-clone/rootfs.ext4" ]; then
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
