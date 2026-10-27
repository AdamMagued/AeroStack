#!/usr/bin/env bash
set -euo pipefail

DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$DIR"

echo "Running OrbStack Free Micro-benchmarks..."
CGO_ENABLED=1 go run .
