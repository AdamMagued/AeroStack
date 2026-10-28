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

if [ -n "$VMD_PID" ]; then
    if ! python3 -c "
import socket, sys
try:
    s = socket.socket(socket.AF_UNIX)
    s.settimeout(1.0)
    s.connect('$HOME/.aerostack/run/docker.sock')
    s.sendall(b'GET /_ping HTTP/1.1\r\nHost: localhost\r\n\r\n')
    resp = s.recv(1024)
    s.close()
    if b'200 OK' in resp or b'OK' in resp:
        sys.exit(0)
except Exception:
    pass
sys.exit(1)
" 2>/dev/null; then
        echo "Stale orb-vmd detected (PID $VMD_PID), terminating..."
        kill -9 "$VMD_PID" 2>/dev/null || true
        VMD_PID=""
    fi
fi

if [ -z "$VMD_PID" ]; then
    rm -f "$HOME/.aerostack/run/docker.sock" "$HOME/.aerostack/run/docker_backend.sock"
    echo "--> Starting orb-vmd for Compose test..."
    "$REPO_ROOT/build/orb-vmd" >/tmp/orb-vmd-test-compose.log 2>&1 &
    VMD_PID=$!
    STARTED_VMD=1
    for i in $(seq 1 120); do
        if python3 -c "
import socket, sys
try:
    s = socket.socket(socket.AF_UNIX)
    s.settimeout(1.0)
    s.connect('$HOME/.aerostack/run/docker.sock')
    s.sendall(b'GET /_ping HTTP/1.1\r\nHost: localhost\r\n\r\n')
    resp = s.recv(1024)
    s.close()
    if b'200 OK' in resp or b'OK' in resp:
        sys.exit(0)
except Exception:
    pass
sys.exit(1)
" 2>/dev/null; then
            echo "AeroStack Docker daemon reachable via /_ping."
            break
        fi
        sleep 0.5
    done
    docker --context aerostack version >/dev/null 2>&1 || true

    # Verify alpine:latest image availability; load from build cache if not already present
    if ! docker --context aerostack image inspect alpine:latest >/dev/null 2>&1; then
        if [ -f "$REPO_ROOT/build/alpine.tar" ]; then
            echo "--> Loading alpine:latest image into AeroStack daemon..."
            docker --context aerostack load -i "$REPO_ROOT/build/alpine.tar" >/dev/null 2>&1 || true
        fi
    fi
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
    pull_policy: if_not_present
    command: sh -c "nc -lk -p 6379 -e /bin/sh -c 'echo +PONG'"
    ports:
      - "127.0.0.1:16379:6379"

  worker:
    image: alpine:latest
    pull_policy: if_not_present
    depends_on:
      - cache
    command: sh -c "for i in \$(seq 1 30); do if echo PING | nc -w 2 cache 6379 | grep -q PONG; then echo 'CACHE_REACHABLE'; exit 0; fi; sleep 0.2; done; exit 1"
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
# Verify TCP port is actively forwarded and communicates with container
python3 -c "
import socket, time

received = b''
for attempt in range(15):
    try:
        s = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
        s.settimeout(2.0)
        s.connect(('127.0.0.1', 16379))
        s.sendall(b'PING\n')
        received = s.recv(1024)
        s.close()
        if b'PONG' in received:
            print(f'Host TCP port forward verified: received {received.decode().strip()}')
            break
    except Exception as e:
        time.sleep(0.5)

assert b'PONG' in received, f'Expected PONG from 127.0.0.1:16379, got: {received}'
"

echo "=== [TEST] Docker Compose Multi-Container Stack PASSED 100% ==="
