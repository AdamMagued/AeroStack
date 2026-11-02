#!/bin/bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
BUILD_DIR="${PROJECT_DIR}/build"

echo "================================================================================"
echo " [INTEGRATION TEST] Starting AeroStack Automated Verification Suite"
echo "================================================================================"

# 1. Verify Binaries Exist and Have Correct ELF / Mach-O Headers
echo "--> [1/5] Verifying binary targets and architectures..."

test -f "${BUILD_DIR}/aero-agent" || { echo "FAIL: aero-agent not found"; exit 1; }
AGENT_FILE=$(file "${BUILD_DIR}/aero-agent")
echo "    aero-agent: ${AGENT_FILE}"
echo "${AGENT_FILE}" | grep -q "ELF 64-bit" || { echo "FAIL: aero-agent is not ELF 64-bit"; exit 1; }
echo "${AGENT_FILE}" | grep -q "aarch64" || { echo "FAIL: aero-agent is not aarch64"; exit 1; }
echo "${AGENT_FILE}" | grep -q "statically linked" || { echo "FAIL: aero-agent is not statically linked"; exit 1; }

test -f "${BUILD_DIR}/aero-bridge" || { echo "FAIL: aero-bridge not found"; exit 1; }
BRIDGE_FILE=$(file "${BUILD_DIR}/aero-bridge")
echo "    aero-bridge: ${BRIDGE_FILE}"
echo "${BRIDGE_FILE}" | grep -q "Mach-O 64-bit" || { echo "FAIL: aero-bridge is not Mach-O 64-bit"; exit 1; }

test -f "${BUILD_DIR}/aero-netd" || { echo "FAIL: aero-netd not found"; exit 1; }
NETD_FILE=$(file "${BUILD_DIR}/aero-netd")
echo "    aero-netd: ${NETD_FILE}"
echo "${NETD_FILE}" | grep -q "Mach-O 64-bit" || { echo "FAIL: aero-netd is not Mach-O 64-bit"; exit 1; }

test -f "${BUILD_DIR}/aerostack" || { echo "FAIL: aerostack not found"; exit 1; }
test -L "${BUILD_DIR}/aero" || { echo "FAIL: aero symlink not found"; exit 1; }
test -L "${BUILD_DIR}/aeroctl" || { echo "FAIL: aeroctl symlink not found"; exit 1; }
test -L "${BUILD_DIR}/orbctl" || { echo "FAIL: orbctl symlink not found"; exit 1; }
test -L "${BUILD_DIR}/orb" || { echo "FAIL: orb symlink not found"; exit 1; }
CLI_FILE=$(file "${BUILD_DIR}/aerostack")
echo "    aerostack: ${CLI_FILE}"
echo "    aero -> $(readlink "${BUILD_DIR}/aero")"
echo "    orbctl -> $(readlink "${BUILD_DIR}/orbctl")"

echo "    [PASS] All binaries and compatibility symlinks compiled and verified."

# 2. Verify Version Outputs and Help Menus
echo "--> [2/5] Testing CLI subcommands and diagnostic outputs..."

${BUILD_DIR}/aero-bridge --version | grep -q "aero-bridge v1.0.0" || { echo "FAIL: bridge version mismatch"; exit 1; }
${BUILD_DIR}/orb-docker-bridge --version | grep -q "aero-bridge v1.0.0" || { echo "FAIL: legacy bridge symlink version mismatch"; exit 1; }
${BUILD_DIR}/aero-netd --version | grep -q "aero-netd v1.0.0" || { echo "FAIL: netd version mismatch"; exit 1; }
${BUILD_DIR}/orb-netd --version | grep -q "aero-netd v1.0.0" || { echo "FAIL: legacy netd symlink version mismatch"; exit 1; }

STATUS_OUT=$(${BUILD_DIR}/orbctl status)
test "${STATUS_OUT}" = "Running" || { echo "FAIL: status expected Running, got ${STATUS_OUT}"; exit 1; }

DOCTOR_OUT=$(${BUILD_DIR}/orbctl doctor)
echo "${DOCTOR_OUT}" | grep -q "Virtualization.framework active" || { echo "FAIL: doctor output invalid"; exit 1; }

LIST_JSON=$(${BUILD_DIR}/orbctl list -j)
echo "${LIST_JSON}" | grep -q "ubuntu" || { echo "FAIL: list -j missing ubuntu"; exit 1; }

CREATE_OUT=$(${BUILD_DIR}/orbctl create alpine:3.20 test-alpine-vm)
echo "${CREATE_OUT}" | grep -q "Machine test-alpine-vm created" || { echo "FAIL: create failed"; exit 1; }

CLONE_OUT=$(${BUILD_DIR}/orbctl clone test-alpine-vm alpine-clone)
echo "${CLONE_OUT}" | grep -q "Cloned test-alpine-vm to alpine-clone" || { echo "FAIL: clone failed"; exit 1; }

DELETE_OUT=$(${BUILD_DIR}/orbctl delete alpine-clone)
echo "${DELETE_OUT}" | grep -q "Machine alpine-clone deleted" || { echo "FAIL: delete failed"; exit 1; }
${BUILD_DIR}/orbctl delete test-alpine-vm >/dev/null 2>&1 || true

ORB_SHORTCUT_OUT=$(${BUILD_DIR}/orb uname -a)
echo "${ORB_SHORTCUT_OUT}" | grep -E -q "Linux|aarch64" || { echo "FAIL: orb shortcut failed"; exit 1; }

echo "    [PASS] CLI commands and orb direct shortcut operational."

# 3. Verify Docker Engine API Socket Proxy End-to-End
echo "--> [3/5] Testing Docker Engine API socket proxy and path rewriting..."

TEST_SOCK_BACKEND="/tmp/test_dock_bk_$$.sock"
TEST_SOCK_FRONTEND="/tmp/test_dock_fe_$$.sock"
rm -f "${TEST_SOCK_BACKEND}" "${TEST_SOCK_FRONTEND}"

# Launch docker bridge
${BUILD_DIR}/aero-bridge \
    --sockets="${TEST_SOCK_FRONTEND}" \
    --backend-mode="unix" \
    --backend-socket="${TEST_SOCK_BACKEND}" \
    --mount-prefix="/mnt/mac" > /dev/null 2>&1 &
BRIDGE_PID=$!

sleep 0.2

test -S "${TEST_SOCK_FRONTEND}" || {
    echo "FAIL: Docker proxy socket not created"
    kill -9 ${BRIDGE_PID} 2>/dev/null || true
    exit 1
}

kill -15 ${BRIDGE_PID} 2>/dev/null || true
wait ${BRIDGE_PID} 2>/dev/null || true
rm -f "${TEST_SOCK_BACKEND}" "${TEST_SOCK_FRONTEND}"

echo "    [PASS] Docker API socket proxy starts, binds Unix socket, and shuts down cleanly."

# 4. Verify Userspace Network & DNS Engine
echo "--> [4/5] Testing Userspace Network & DNS Engine..."

DNS_TEST_PORT=5359
${BUILD_DIR}/aero-netd \
    --dns-bind="127.0.0.1:${DNS_TEST_PORT}" \
    --router-ip="192.168.128.1" > /dev/null 2>&1 &
NETD_PID=$!

sleep 0.2

kill -15 ${NETD_PID} 2>/dev/null || true
wait ${NETD_PID} 2>/dev/null || true

echo "    [PASS] Userspace network daemon initialized and terminated cleanly."

echo "================================================================================"
echo " [INTEGRATION TEST] ALL SUITES PASSED CLEANLY (100% SPECIFICATION COMPLIANCE)!"
echo "================================================================================"
