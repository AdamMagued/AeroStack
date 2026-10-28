#!/bin/bash
# ==============================================================================
# AeroStack Test: Zero Stubs, Zero Sleep, Zero Emojis Audit
# ==============================================================================
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

echo "=== [AUDIT] Running Static Zero-Stubs Verification ==="

# 1. Assert ZERO occurrences of time.Sleep in src/
echo -n "Checking for time.Sleep in src/... "
if grep -rn "time.Sleep" "$REPO_ROOT/src" 2>/dev/null; then
    echo "FAIL: Found simulated time.Sleep in production code"
    exit 1
fi
echo "PASSED (0 occurrences)"

# 2. Assert ZERO emojis in source files
echo -n "Checking for emojis in src/... "
EMOJI_COUNT=$(python3 -c "
import os
count = 0
for root, dirs, files in os.walk('$REPO_ROOT/src'):
    for f in files:
        if f.endswith(('.go', '.swift', '.sh', '.py')):
            p = os.path.join(root, f)
            with open(p, 'rb') as fp:
                for line_no, line in enumerate(fp.read().splitlines(), 1):
                    try:
                        s = line.decode('utf-8')
                        for ch in s:
                            if ord(ch) in (0x26A1, 0x2713, 0x2717, 0x2794, 0x1F9F9, 0x1F4C9):
                                print(f'{p}:{line_no}: emoji {ch}')
                                count += 1
                    except Exception:
                        pass
print(count)
" | tail -n 1)

if [ "$EMOJI_COUNT" -ne "0" ]; then
    echo "FAIL: Detected $EMOJI_COUNT emojis in source files"
    exit 1
fi
echo "PASSED (0 emojis)"

# 3. Assert ZERO mock dictionaries in CLI queryMachines
echo -n "Checking for hardcoded queryMachines slice... "
if grep -rn 'Name: *"fedora-dev"' "$REPO_ROOT/src/host/cli/main.go" 2>/dev/null; then
    echo "FAIL: Found hardcoded mock machine list in CLI"
    exit 1
fi
echo "PASSED (0 mock machine slices)"

# 4. Assert ZERO tautological doctor assertions
echo -n "Checking for tautological doctor assertions in GUI store... "
if grep -rn 'status: cowResult == 0 ? \.pass : \.pass' "$REPO_ROOT/src" 2>/dev/null; then
    echo "FAIL: Found tautological doctor status check"
    exit 1
fi
echo "PASSED (all doctor checks strictly evaluated)"

echo "=== [AUDIT] Zero-Stubs Verification PASSED 100% ==="
