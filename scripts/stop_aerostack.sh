#!/bin/bash
# ==============================================================================
# AeroStack Daemon Stopper
# Stops all AeroStack host daemons cleanly.
# ==============================================================================
set -euo pipefail

echo "Stopping AeroStack daemons..."
PIDS=$(pgrep -f "aero-vmd|orb-vmd|aero-bridge|orb-docker-bridge|aero-netd|orb-netd" || true)

if [ -n "$PIDS" ]; then
    kill -15 $PIDS 2>/dev/null || true
    sleep 0.5
    STILL_RUNNING=$(pgrep -f "aero-vmd|orb-vmd|aero-bridge|orb-docker-bridge|aero-netd|orb-netd" || true)
    if [ -n "$STILL_RUNNING" ]; then
        kill -9 $STILL_RUNNING 2>/dev/null || true
    fi
    echo "AeroStack daemons stopped."
else
    echo "No running AeroStack daemons found."
fi

# Clean up stale unix sockets
rm -f "${HOME}/.aerostack/run/"*.sock 2>/dev/null || true
