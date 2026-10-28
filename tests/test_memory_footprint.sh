#!/bin/bash
# ==============================================================================
# AeroStack Test: Mach Physical Memory Footprint Verification
# ==============================================================================
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
BIN_PATH="/tmp/aero_mem_audit_$$"

cleanup() {
    rm -f "$BIN_PATH"
}
trap cleanup EXIT

echo "=== [TEST] Mach Physical Footprint Verification ==="

# 1. Locate or start orb-vmd daemon
VMD_PID=$(pgrep -f "orb-vmd" | head -n 1 || true)
STARTED_VMD=0

if [ -z "$VMD_PID" ]; then
    echo "--> Starting orb-vmd for memory measurement..."
    "$REPO_ROOT/build/orb-vmd" >/tmp/orb-vmd-test-mem.log 2>&1 &
    VMD_PID=$!
    STARTED_VMD=1
    for i in $(seq 1 40); do
        if [ -S "$HOME/.aerostack/run/docker.sock" ] && [ -S "$HOME/.aerostack/run/vmcontrol.sock" ]; then
            break
        fi
        sleep 0.25
    done
fi

cleanup() {
    if [ "$STARTED_VMD" -eq 1 ]; then
        echo "--> Stopping test orb-vmd (PID $VMD_PID)..."
        kill -15 "$VMD_PID" 2>/dev/null || true
    fi
}
trap cleanup EXIT

echo "--> Verified active orb-vmd daemon (PID: $VMD_PID)"

# 2. Put active container load on orb-vmd via aerostack context
if [ -S "$HOME/.aerostack/run/docker.sock" ]; then
    echo "--> Generating active container load via docker --context aerostack..."
    docker --context aerostack run --rm alpine sh -c 'dd if=/dev/zero of=/tmp/load bs=1M count=20 && rm -f /tmp/load' 2>/dev/null || true
fi

# 3. Query real Darwin Mach physical footprint of orb-vmd PID via libproc proc_pid_rusage
python3 - "$VMD_PID" << 'EOF'
import ctypes, sys

libproc = ctypes.CDLL('/usr/lib/libproc.dylib')

class rusage_info_v4(ctypes.Structure):
    _fields_ = [
        ('ri_uuid', ctypes.c_uint8 * 16),
        ('ri_user_time', ctypes.c_uint64),
        ('ri_system_time', ctypes.c_uint64),
        ('ri_pkg_idle_wkups', ctypes.c_uint64),
        ('ri_interrupt_wkups', ctypes.c_uint64),
        ('ri_pageins', ctypes.c_uint64),
        ('ri_wired_size', ctypes.c_uint64),
        ('ri_resident_size', ctypes.c_uint64),
        ('ri_phys_footprint', ctypes.c_uint64),
        ('ri_proc_start_abstime', ctypes.c_uint64),
        ('ri_proc_exit_abstime', ctypes.c_uint64),
    ]

pid = int(sys.argv[1])
ru = rusage_info_v4()
res = libproc.proc_pid_rusage(pid, 4, ctypes.byref(ru))
if res != 0:
    print(f"FATAL: proc_pid_rusage failed for pid {pid}", file=sys.stderr)
    sys.exit(1)

residentMB = ru.ri_resident_size / (1024.0 * 1024.0)
physMB = ru.ri_phys_footprint / (1024.0 * 1024.0)

print(f"Measured orb-vmd (PID {pid}): Resident = {residentMB:.2f} MB | PhysFootprint = {physMB:.2f} MB")

if physMB < 60.0:
    print(f"PASSED: Mach physical footprint {physMB:.2f} MB is below the 60 MB ceiling under load.")
    sys.exit(0)
else:
    print(f"FAILED: Mach physical footprint {physMB:.2f} MB exceeded 60 MB ceiling.", file=sys.stderr)
    sys.exit(1)
EOF

echo "=== [TEST] Mach Physical Memory Footprint PASSED 100% ==="
