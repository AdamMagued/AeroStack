#!/bin/bash
# ==============================================================================
# AeroStack Daemon Launcher
# Starts orb-vmd in the background and verifies readiness.
# ==============================================================================
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
AERO_SOCK="${HOME}/.aerostack/run/docker.sock"
VM_SOCK="${HOME}/.aerostack/run/vmcontrol.sock"
LOG_DIR="${HOME}/.aerostack/log"
mkdir -p "$LOG_DIR"

if pgrep -f "aero-vmd|orb-vmd" >/dev/null 2>&1 && [ -S "$AERO_SOCK" ] && DOCKER_HOST="unix://$AERO_SOCK" docker version >/dev/null 2>&1; then
    echo "AeroStack is already running and dockerd is responsive."
    exit 0
fi

VMD_BIN="$REPO_ROOT/build/aero-vmd"
if [ ! -f "$VMD_BIN" ]; then
    VMD_BIN="$REPO_ROOT/build/orb-vmd"
fi

echo "Starting AeroStack daemon (aero-vmd)..."
nohup "$VMD_BIN" > "$LOG_DIR/aero-vmd.log" 2>&1 &
VMD_PID=$!
echo "AeroStack started with PID $VMD_PID. Waiting for sockets and dockerd responsiveness..."

for i in $(seq 1 60); do
    if [ -S "$AERO_SOCK" ] && [ -S "$VM_SOCK" ] && DOCKER_HOST="unix://$AERO_SOCK" docker version >/dev/null 2>&1; then
        echo "AeroStack is ready! Docker socket: $AERO_SOCK"
        exit 0
    fi
    sleep 0.5
done

echo "Timed out waiting for AeroStack sockets and dockerd responsiveness. Check $LOG_DIR/aero-vmd.log"
exit 1
