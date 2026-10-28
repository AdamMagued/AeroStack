#!/bin/bash
# ==============================================================================
# AeroStack Test: Apple Silicon Rosetta 2 Linux Binary Translation
# ==============================================================================
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

echo "=== [TEST] Apple Silicon Rosetta 2 Linux Verification ==="

# 1. Verify Darwin host Virtualization.framework Rosetta availability
echo -n "Checking Darwin VZLinuxRosettaDirectoryShare... "
ROSETTA_STATUS=$(swift -e '
import Virtualization
if #available(macOS 13.0, *) {
    switch VZLinuxRosettaDirectoryShare.availability {
    case .installed: print("INSTALLED")
    case .notInstalled: print("NOT_INSTALLED")
    case .notSupported: print("NOT_SUPPORTED")
    @unknown default: print("UNKNOWN")
    }
} else {
    print("UNSUPPORTED_OS")
}
')

if [ "$ROSETTA_STATUS" != "INSTALLED" ]; then
    echo "FAIL: Rosetta status is $ROSETTA_STATUS (expected INSTALLED)"
    exit 1
fi
echo "PASSED (Rosetta 2 Linux translation runtime installed on macOS)"

# 2. Verify VMManager.swift wires VZLinuxRosettaDirectoryShare
echo -n "Verifying VMManager.swift directory sharing configuration... "
if ! grep -q "VZLinuxRosettaDirectoryShare" "$REPO_ROOT/src/host/engine/VMManager.swift"; then
    echo "FAIL: VZLinuxRosettaDirectoryShare not found in VMManager.swift"
    exit 1
fi
if ! grep -q 'tag: "rosetta"' "$REPO_ROOT/src/host/engine/VMManager.swift"; then
    echo "FAIL: Rosetta VirtioFS tag 'rosetta' not found in VMManager.swift"
    exit 1
fi
echo "PASSED (VMManager.swift wires tag 'rosetta')"

# 3. Verify guest init script mounts rosetta and registers binfmt_misc
echo -n "Verifying guest asset packaging script binfmt_misc registration... "
if ! grep -q "mount -t virtiofs rosetta" "$REPO_ROOT/scripts/build_guest_assets.sh"; then
    echo "FAIL: Rosetta virtiofs mount missing from build_guest_assets.sh"
    exit 1
fi
if ! grep -q "/proc/sys/fs/binfmt_misc/register" "$REPO_ROOT/scripts/build_guest_assets.sh"; then
    echo "FAIL: binfmt_misc registration missing from build_guest_assets.sh"
    exit 1
fi
echo "PASSED (guest init registers binfmt_misc ELF x86_64 translator)"

# 4. Empirical verification: execute amd64 container binary and assert x86_64 output
echo -n "Executing empirical x86_64 container binary: "
ARCH_OUTPUT=$(docker run --rm --platform linux/amd64 alpine:latest uname -m | tr -d '\r\n')
echo "uname -m -> $ARCH_OUTPUT"

if [ "$ARCH_OUTPUT" != "x86_64" ]; then
    echo "FAIL: Expected 'x86_64', got '$ARCH_OUTPUT'"
    exit 1
fi
echo "PASSED: x86_64 Linux translation empirically verified"

echo "=== [TEST] Apple Silicon Rosetta 2 Verification PASSED 100% ==="
