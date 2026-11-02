#!/bin/bash
# ==============================================================================
# AeroStack Rollback Script
# Safely reverts the Docker environment from AeroStack back to OrbStack.
# ==============================================================================
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DOCKER_RUN_DIR="${HOME}/.docker/run"
DOCKER_SOCK="${DOCKER_RUN_DIR}/docker.sock"
ORB_SOCK="${HOME}/.orbstack/run/docker.sock"

echo "======================================================================"
echo " AeroStack Rollback to OrbStack"
echo "======================================================================"

# 1. Restore Docker socket symlink
echo "--> 1. Restoring Docker socket to OrbStack..."
mkdir -p "$DOCKER_RUN_DIR"
rm -f "$DOCKER_SOCK"

if [ -e "${DOCKER_SOCK}.orbstack.bak" ]; then
    cp -P "${DOCKER_SOCK}.orbstack.bak" "$DOCKER_SOCK"
    rm -f "${DOCKER_SOCK}.orbstack.bak"
    echo "    Restored original socket symlink from backup."
elif [ -e "$ORB_SOCK" ]; then
    ln -sf "$ORB_SOCK" "$DOCKER_SOCK"
    echo "    Symlinked $DOCKER_SOCK -> $ORB_SOCK"
else
    echo "    Warning: OrbStack socket $ORB_SOCK not found."
fi

# 2. Revert Docker context
echo "--> 2. Switching Docker CLI context..."
if docker context inspect orbstack >/dev/null 2>&1; then
    docker context use orbstack
    echo "    Switched default context to 'orbstack'."
elif docker context inspect default >/dev/null 2>&1; then
    docker context use default
    echo "    Switched default context to 'default'."
fi

# 3. Verification
echo "--> 3. Verifying Docker connectivity..."
if docker version >/dev/null 2>&1; then
    CURRENT_SERVER=$(docker version --format '{{.Server.Version}}' 2>/dev/null || echo "Unknown")
    echo "    Connected to Docker Engine: ${CURRENT_SERVER}"
else
    echo "    Warning: Docker daemon is currently unreachable. Launch OrbStack to resume."
fi

echo "======================================================================"
echo " ROLLBACK COMPLETE."
echo "======================================================================"
