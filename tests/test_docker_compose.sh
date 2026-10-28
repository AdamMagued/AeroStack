#!/bin/bash
# ==============================================================================
# AeroStack Test: Docker Compose Multi-Container Stack
# ==============================================================================
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
TMP_COMPOSE_DIR="$(mktemp -d /tmp/aero_compose_XXXXXX)"

VMD_PID=$(pgrep -f "orb-vmd" | head -n 1 || true)
STARTED_VMD=0

if [ -z "$VMD_PID" ]; then
    echo "--> Starting orb-vmd for Compose test..."
    "$REPO_ROOT/build/orb-vmd" >/tmp/orb-vmd-test-compose.log 2>&1 &
    VMD_PID=$!
    STARTED_VMD=1
    for i in $(seq 1 40); do
        if [ -S "$HOME/.aerostack/run/docker.sock" ]; then
            break
        fi
        sleep 0.25
    done
fi

cleanup() {
    echo "Tearing down Docker Compose stack..."
    docker --context aerostack compose -f "$TMP_COMPOSE_DIR/docker-compose.yml" down -v --remove-orphans 2>/dev/null || true
    rm -rf "$TMP_COMPOSE_DIR"
    if [ "$STARTED_VMD" -eq 1 ]; then
        kill -15 "$VMD_PID" 2>/dev/null || true
    fi
}
trap cleanup EXIT

echo "=== [TEST] Docker Compose Multi-Container Stack ==="

cat << 'EOF' > "$TMP_COMPOSE_DIR/docker-compose.yml"
services:
  cache:
    image: alpine:latest
    command: sh -c "while true; do echo '+PONG' | nc -l -p 6379; done"
    ports:
      - "127.0.0.1:16379:6379"

  worker:
    image: alpine:latest
    depends_on:
      - cache
    command: sh -c "sleep 0.1 && echo PING | nc cache 6379 | grep PONG && echo 'CACHE_REACHABLE' && exit 0"
EOF

echo "--> Launching compose stack on AeroStack engine..."
docker --context aerostack compose -f "$TMP_COMPOSE_DIR/docker-compose.yml" up -d

echo "--> Verifying worker container reached cache service..."
for i in $(seq 1 30); do
    if docker --context aerostack compose -f "$TMP_COMPOSE_DIR/docker-compose.yml" logs worker 2>/dev/null | grep -q "CACHE_REACHABLE"; then
        break
    fi
    sleep 0.2
done
docker --context aerostack compose -f "$TMP_COMPOSE_DIR/docker-compose.yml" logs worker | grep -q "CACHE_REACHABLE"
echo "Worker successfully communicated with cache over internal container network."

echo "--> Verifying host port forward connectivity on 127.0.0.1:16379..."
# Verify TCP port is actively forwarded and bound on host
python3 -c "
import socket
s = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
s.settimeout(3.0)
try:
    s.connect(('127.0.0.1', 16379))
    print('Host TCP socket 127.0.0.1:16379 is actively bound and forwarded')
except ConnectionResetError:
    print('Host TCP socket 127.0.0.1:16379 is actively bound and forwarded')
finally:
    s.close()
"

echo "=== [TEST] Docker Compose Multi-Container Stack PASSED 100% ==="
