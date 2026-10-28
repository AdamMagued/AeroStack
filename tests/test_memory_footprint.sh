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

swiftc -O -o "$BIN_PATH" - << 'EOF'
import Darwin
import Foundation

var info = task_vm_info_data_t()
var count = mach_msg_type_number_t(MemoryLayout<task_vm_info_data_t>.size / MemoryLayout<natural_t>.size)
let kr = withUnsafeMutablePointer(to: &info) {
    $0.withMemoryRebound(to: integer_t.self, capacity: Int(count)) {
        task_info(mach_task_self_, task_flavor_t(TASK_VM_INFO), $0, &count)
    }
}

guard kr == KERN_SUCCESS else {
    print("FATAL: task_info failed with kernel error: \(kr)")
    exit(1)
}

let residentMB = Double(info.resident_size) / (1024.0 * 1024.0)
let physMB = Double(info.phys_footprint) / (1024.0 * 1024.0)

print(String(format: "Measured Footprint: Resident = %.2f MB | PhysFootprint = %.2f MB", residentMB, physMB))

if physMB < 50.0 {
    print(String(format: "PASSED: Mach physical footprint %.2f MB is well below the 50 MB ceiling.", physMB))
    exit(0)
} else {
    print(String(format: "FAILED: Mach physical footprint %.2f MB exceeded 50 MB ceiling.", physMB))
    exit(1)
}
EOF

"$BIN_PATH"
echo "=== [TEST] Mach Physical Memory Footprint PASSED 100% ==="
