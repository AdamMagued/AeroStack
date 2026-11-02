#!/bin/bash
# ==============================================================================
# AeroStack Production Installation Script
# https://github.com/AdamMagued/AeroStack
# ==============================================================================
set -euo pipefail

REPO="AdamMagued/AeroStack"
VERSION="${AEROSTACK_VERSION:-v0.1.0}"
RELEASE_BASE="https://github.com/${REPO}/releases/download/${VERSION}"

echo "======================================================================"
echo " AeroStack Automated Production Installer (${VERSION})"
echo "======================================================================"

# 1. System Architecture and OS Validation
OS_NAME="$(uname -s)"
ARCH_NAME="$(uname -m)"

if [ "$OS_NAME" != "Darwin" ]; then
    echo "ERROR: AeroStack requires macOS (Darwin). Detected OS: $OS_NAME" >&2
    exit 1
fi

if [ "$ARCH_NAME" != "arm64" ]; then
    echo "ERROR: AeroStack is optimized strictly for Apple Silicon (ARM64). Detected: $ARCH_NAME" >&2
    exit 1
fi

DARWIN_MAJOR="$(uname -r | cut -d. -f1)"
if [ "$DARWIN_MAJOR" -lt 22 ]; then
    echo "ERROR: AeroStack requires macOS 13 (Ventura) or newer (Darwin 22+). Detected: Darwin $DARWIN_MAJOR" >&2
    exit 1
fi

# 2. Determine Installation Directories
if [ -w "/usr/local/bin" ]; then
    BIN_DIR="/usr/local/bin"
elif [ -d "/usr/local/bin" ] && sudo -n true 2>/dev/null; then
    BIN_DIR="/usr/local/bin"
    SUDO="sudo"
else
    BIN_DIR="${HOME}/.aerostack/bin"
    mkdir -p "$BIN_DIR"
fi

CACHE_DIR="${HOME}/.aerostack/cache"
RUN_DIR="${HOME}/.aerostack/run"
mkdir -p "$CACHE_DIR" "$RUN_DIR"

TMP_DIR="$(mktemp -d /tmp/aerostack_install_XXXXXX)"
cleanup() {
    rm -rf "$TMP_DIR"
}
trap cleanup EXIT

# 3. Download Release Archives & Checksums
echo "--> Downloading release assets for ${VERSION}..."
DARWIN_ARCHIVE="aerostack-${VERSION}-darwin-arm64.tar.gz"
LINUX_ARCHIVE="aerostack-${VERSION}-linux-arm64.tar.gz"
CHECKSUMS="checksums.sha256"

curl -sSL -f "${RELEASE_BASE}/${DARWIN_ARCHIVE}" -o "${TMP_DIR}/${DARWIN_ARCHIVE}"
curl -sSL -f "${RELEASE_BASE}/${LINUX_ARCHIVE}" -o "${TMP_DIR}/${LINUX_ARCHIVE}"
curl -sSL -f "${RELEASE_BASE}/${CHECKSUMS}" -o "${TMP_DIR}/${CHECKSUMS}"

# 4. Checksum Verification
echo "--> Verifying SHA256 checksums..."
(
    cd "$TMP_DIR"
    shasum -a 256 -c "$CHECKSUMS"
)
echo "    Checksum verification passed 100%."

# 5. Extract and Install Host Binaries
echo "--> Installing host engine and CLI binaries to ${BIN_DIR}..."
tar -xzf "${TMP_DIR}/${DARWIN_ARCHIVE}" -C "$TMP_DIR"

HOST_BINARIES=("aero-vmd" "aero-bridge" "aero-netd" "aerostack")
for bin in "${HOST_BINARIES[@]}"; do
    if [ -n "${SUDO:-}" ]; then
        $SUDO cp -f "${TMP_DIR}/${bin}" "${BIN_DIR}/${bin}"
        $SUDO chmod 755 "${BIN_DIR}/${bin}"
    else
        cp -f "${TMP_DIR}/${bin}" "${BIN_DIR}/${bin}"
        chmod 755 "${BIN_DIR}/${bin}"
    fi
done

# Codesign aero-vmd with entitlements for hypervisor virtualization
if [ -f "${TMP_DIR}/entitlements.plist" ]; then
    if [ -n "${SUDO:-}" ]; then
        $SUDO codesign --force --sign - --entitlements "${TMP_DIR}/entitlements.plist" "${BIN_DIR}/aero-vmd" 2>/dev/null || true
    else
        codesign --force --sign - --entitlements "${TMP_DIR}/entitlements.plist" "${BIN_DIR}/aero-vmd" 2>/dev/null || true
    fi
fi

# Compatibility Symlinks
SYMLINKS=("aero:aerostack" "aeroctl:aerostack" "orb:aerostack" "orbctl:aerostack" "orb-vmd:aero-vmd" "orb-docker-bridge:aero-bridge" "orb-netd:aero-netd")
for item in "${SYMLINKS[@]}"; do
    link="${item%%:*}"
    target="${item##*:}"
    if [ -n "${SUDO:-}" ]; then
        $SUDO ln -sf "$target" "${BIN_DIR}/${link}"
    else
        ln -sf "$target" "${BIN_DIR}/${link}"
    fi
done

# 6. Extract Guest Assets into Cache
echo "--> Installing Linux guest assets to ${CACHE_DIR}..."
tar -xzf "${TMP_DIR}/${LINUX_ARCHIVE}" -C "$CACHE_DIR"
chmod 644 "${CACHE_DIR}/vmlinux" "${CACHE_DIR}/initramfs-virt" 2>/dev/null || true
chmod 644 "${CACHE_DIR}/rootfs.ext4" 2>/dev/null || true
chmod 755 "${CACHE_DIR}/aero-agent" 2>/dev/null || true
if [ -f "${CACHE_DIR}/aero-agent" ]; then
    ln -sf aero-agent "${CACHE_DIR}/orb-agent" 2>/dev/null || true
fi

# 7. Setup Docker CLI Context
if command -v docker >/dev/null 2>&1; then
    echo "--> Configuring Docker CLI context 'aerostack'..."
    docker context create aerostack --docker "host=unix://${HOME}/.aerostack/run/docker.sock" 2>/dev/null || \
    docker context update aerostack --docker "host=unix://${HOME}/.aerostack/run/docker.sock" 2>/dev/null || true
    docker context use aerostack >/dev/null 2>&1 || true
fi

# 8. PATH Verification
if [[ ":$PATH:" != *":${BIN_DIR}:"* ]]; then
    echo "NOTE: Add ${BIN_DIR} to your PATH by adding this line to ~/.zshrc or ~/.bashrc:"
    echo "    export PATH=\"${BIN_DIR}:\$PATH\""
fi

echo "======================================================================"
echo " AeroStack ${VERSION} successfully installed."
echo " Launch the daemon:    aerostack start"
echo " Run Linux command:    aero uname -a"
echo " Check system status:  aerostack status"
echo "======================================================================"
