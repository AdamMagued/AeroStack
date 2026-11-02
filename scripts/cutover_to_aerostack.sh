#!/bin/bash
# ==============================================================================
# AeroStack Cutover Script
# Seamlessly cuts over the Docker environment from OrbStack to AeroStack.
# Idempotent, safe, and fully verified.
# ==============================================================================
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
AERO_SOCK="${HOME}/.aerostack/run/docker.sock"
DOCKER_RUN_DIR="${HOME}/.docker/run"
DOCKER_SOCK="${DOCKER_RUN_DIR}/docker.sock"

echo "======================================================================"
echo " AeroStack Seamless Production Cutover"
echo "======================================================================"

# 1. Clean AppleDouble files
find "$REPO_ROOT" -name "._*" -delete 2>/dev/null || true

# 2. Pre-flight verification
echo "--> 1. Verifying AeroStack daemon and socket..."
if [ ! -S "$AERO_SOCK" ]; then
    echo "ERROR: AeroStack socket not found at $AERO_SOCK"
    echo "Ensure aero-vmd daemon is running."
    exit 1
fi

AERO_VERSION=$(docker --context aerostack version --format '{{.Server.Version}}' 2>/dev/null || echo "")
if [ -z "$AERO_VERSION" ]; then
    echo "ERROR: Unable to communicate with AeroStack Docker daemon via context 'aerostack'."
    exit 1
fi
echo "    Connected to AeroStack Engine (version: ${AERO_VERSION})"

echo "--> 2. Verifying required container images..."
if ! docker --context aerostack image inspect pr-sandbox:latest >/dev/null 2>&1; then
    echo "ERROR: pr-sandbox:latest is not loaded into AeroStack image store."
    exit 1
fi
echo "    Verified image: pr-sandbox:latest"

# 3. Socket link migration
echo "--> 3. Updating default Docker socket symlink..."
mkdir -p "$DOCKER_RUN_DIR"
if [ -L "$DOCKER_SOCK" ] || [ -S "$DOCKER_SOCK" ]; then
    if [ ! -e "${DOCKER_SOCK}.orbstack.bak" ]; then
        cp -P "$DOCKER_SOCK" "${DOCKER_SOCK}.orbstack.bak" 2>/dev/null || true
    fi
    rm -f "$DOCKER_SOCK"
fi
ln -sf "$AERO_SOCK" "$DOCKER_SOCK"
echo "    Symlinked $DOCKER_SOCK -> $AERO_SOCK"

# 4. Context migration
echo "--> 4. Setting default Docker CLI context to aerostack..."
docker context use aerostack

# 5. End-to-end verification via AutoWorkflow sandbox runner
echo "--> 5. Running end-to-end validation through AutoWorkflow runner..."
RUNNER="/Volumes/AdamSSD/antigravity_workspace/AutoWorkflow/sandbox/run_in_sandbox.sh"
if [ -x "$RUNNER" ]; then
    OUTPUT=$("$RUNNER" "echo CUTOVER_SUCCESS && uname -s -m && go version")
    echo "$OUTPUT"
    echo "$OUTPUT" | grep -q "CUTOVER_SUCCESS" || { echo "ERROR: Cutover validation failed"; exit 1; }
    echo "    [PASS] AutoWorkflow sandbox execution verified via AeroStack."
else
    echo "    Notice: $RUNNER not found; testing direct container execution..."
    docker run --rm pr-sandbox:latest -c "echo DIRECT_SUCCESS && go version"
fi

echo "======================================================================"
echo " CUTOVER COMPLETE: AeroStack is now active as the primary Docker engine."
echo " To rollback at any time, run: $SCRIPT_DIR/rollback_to_orbstack.sh"
echo "======================================================================"
