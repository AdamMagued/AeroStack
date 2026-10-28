#!/bin/bash
# ==============================================================================
# AeroStack Test: Docker Compose Multi-Container Stack
# ==============================================================================
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
TMP_COMPOSE_DIR="$(mktemp -d /tmp/aero_compose_XXXXXX)"

cleanup() {
    echo "Tearing down Docker Compose stack..."
    docker compose -f "$TMP_COMPOSE_DIR/docker-compose.yml" down -v --remove-orphans 2>/dev/null || true
    rm -rf "$TMP_COMPOSE_DIR"
}
trap cleanup EXIT

echo "=== [TEST] Docker Compose Multi-Container Stack ==="

cat << 'EOF' > "$TMP_COMPOSE_DIR/docker-compose.yml"
services:
  cache:
    image: redis:7-alpine
    command: redis-server --save "" --appendonly no
    ports:
      - "127.0.0.1:16379:6379"

  worker:
    image: alpine:latest
    depends_on:
      - cache
    command: sh -c "nc -z cache 6379 && echo 'REDIS_REACHABLE' && exit 0"
EOF

echo "--> Launching compose stack..."
docker compose -f "$TMP_COMPOSE_DIR/docker-compose.yml" up --abort-on-container-exit --exit-code-from worker worker

echo "--> Verifying host port forward connectivity on 127.0.0.1:16379..."
# Verify TCP port connectivity from host
python3 -c "
import socket
s = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
s.settimeout(3.0)
s.connect(('127.0.0.1', 16379))
s.sendall(b'PING\r\n')
data = s.recv(1024)
s.close()
assert b'+PONG' in data, f'Unexpected response: {data}'
print('Host TCP socket forwarded PING -> +PONG successfully')
"

echo "=== [TEST] Docker Compose Multi-Container Stack PASSED 100% ==="
