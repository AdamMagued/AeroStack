#!/bin/bash
# ==============================================================================
# AeroStack / orbstack_free Guest Asset Builder
# Produces minimal Linux ARM64 kernel, initramfs, and ext4 rootfs disk image
# All assets strictly stored on external SSD storage.
# ==============================================================================

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
BUILD_DIR="${PROJECT_DIR}/build"
APFS_CACHE="/Volumes/MacStorage/aerostack/cache"
if [ -d "${APFS_CACHE}" ]; then
    CACHE_DIR="${AEROSTACK_CACHE_DIR:-${APFS_CACHE}}"
else
    CACHE_DIR="${AEROSTACK_CACHE_DIR:-${PROJECT_DIR}/aerostack_runtime/cache}"
fi

mkdir -p "${BUILD_DIR}" "${CACHE_DIR}"

echo "=========================================================="
echo " AeroStack Guest Asset Packaging Pipeline"
echo " Target Cache: ${CACHE_DIR}"
echo "=========================================================="

# 1. Compile Linux ARM64 static guest agent
echo "--> Compiling static Linux ARM64 orb-agent binary..."
GOCACHE="${PROJECT_DIR}/.gocache" GOPATH="${PROJECT_DIR}/.gopath" \
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 \
go build -ldflags="-s -w" -o "${BUILD_DIR}/orb-agent" "${PROJECT_DIR}/src/guest/agent/..."

echo "--> Using orb-agent: ${BUILD_DIR}/orb-agent ($(wc -c < "${BUILD_DIR}/orb-agent") bytes)"

# 2. Package guest assets via Alpine Linux builder container
echo "--> Generating vmlinux, initramfs-virt, and rootfs.ext4 via Alpine builder..."

docker run --rm \
    -v "${BUILD_DIR}/orb-agent:/input/orb-agent:ro" \
    -v "${BUILD_DIR}/alpine.tar:/input/alpine.tar:ro" \
    -v "${CACHE_DIR}:/output" \
    alpine:latest sh -c '
set -euo pipefail

echo "  * Configuring high-speed Alpine package mirrors..."
sed -i "s|https://dl-cdn.alpinelinux.org|https://mirrors.edge.kernel.org|g" /etc/apk/repositories
apk update >/dev/null

echo "  * Installing Alpine packaging tools and kernel..."
apk add --no-cache linux-virt e2fsprogs e2fsprogs-extra python3 gzip >/dev/null

echo "  * Extracting uncompressed ARM64 Linux kernel (vmlinux)..."
python3 -c '\''
import struct, gzip
with open("/boot/vmlinuz-virt", "rb") as f:
    data = f.read()
magic, zimg, offset, size = struct.unpack("<4s4sII", data[:16])
if zimg != b"zimg":
    raise ValueError("Not a zboot image")
raw = gzip.decompress(data[offset:offset+size])
with open("/output/vmlinux", "wb") as f_out:
    f_out.write(raw)
'\''

echo "  * Copying initramfs-virt..."
cp /boot/initramfs-virt /output/initramfs-virt

echo "  * Constructing rootfs tree..."
rm -rf /tmp/rootfs
mkdir -p /tmp/rootfs/etc/apk
cp -r /etc/apk/keys /etc/apk/repositories /tmp/rootfs/etc/apk/

apk add --root /tmp/rootfs --initdb --no-cache \
    alpine-base ca-certificates docker containerd runc iptables iproute2 e2fsprogs e2fsprogs-extra util-linux procps >/dev/null

echo "  * Installing and decompressing kernel modules..."
mkdir -p /tmp/rootfs/lib
cp -a /lib/modules /tmp/rootfs/lib/
find /tmp/rootfs/lib/modules -name "*.ko.gz" -exec gzip -d {} +
KVER=$(ls /tmp/rootfs/lib/modules | head -n 1)
depmod -a -b /tmp/rootfs "${KVER}"

echo "  * Installing orb-agent static binary and preloaded assets..."
mkdir -p /tmp/rootfs/usr/local/bin /tmp/rootfs/var/lib/docker-preload
cp /input/orb-agent /tmp/rootfs/usr/local/bin/orb-agent
chmod 755 /tmp/rootfs/usr/local/bin/orb-agent
if [ -f /input/alpine.tar ]; then
    cp /input/alpine.tar /tmp/rootfs/var/lib/docker-preload/alpine.tar
fi

echo "  * Configuring Docker daemon (/etc/docker/daemon.json)..."
mkdir -p /tmp/rootfs/etc/docker
cat << "EOF_DOCKER" > /tmp/rootfs/etc/docker/daemon.json
{
  "storage-driver": "overlay2",
  "live-restore": true,
  "log-driver": "local",
  "log-opts": {
    "max-size": "10m"
  }
}
EOF_DOCKER

echo "  * Configuring containerd (/etc/containerd/config.toml)..."
mkdir -p /tmp/rootfs/etc/containerd
cat << "EOF_CONTAINERD" > /tmp/rootfs/etc/containerd/config.toml
version = 2
[plugins."io.containerd.grpc.v1.cri"]
  sandbox_image = "pause:3.9"
EOF_CONTAINERD

echo "  * Configuring DNS (/etc/resolv.conf)..."
cat << "EOF_RESOLV" > /tmp/rootfs/etc/resolv.conf
nameserver 192.168.64.1
nameserver 1.1.1.1
nameserver 8.8.8.8
EOF_RESOLV

echo "  * Writing guest microVM init script (/sbin/init)..."
rm -f /tmp/rootfs/sbin/init /tmp/rootfs/init
cat << "EOF_INIT" > /tmp/rootfs/sbin/init
#!/bin/sh
# Direct console output to kernel log buffer for host monitoring
exec >/dev/kmsg 2>&1
echo "[AERO-INIT] MicroVM stage 2 init started."

export PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin

# Mount virtual filesystems
mount -t proc proc /proc
mount -t sysfs sys /sys
mount -t devtmpfs dev /dev
mkdir -p /dev/pts /dev/shm
mount -t devpts devpts /dev/pts
mount -t tmpfs tmpfs /dev/shm
mount -t tmpfs tmpfs /tmp
mount -t tmpfs tmpfs /run

# Online resize root filesystem if underlying block device allows
if [ -b /dev/vda ]; then
    echo "[AERO-INIT] Resizing root filesystem on /dev/vda..."
    resize2fs /dev/vda >/dev/kmsg 2>&1 || true
fi

# Remount root with discard if needed for APFS storage reclamation
mount -o remount,rw,discard / 2>/dev/null || true

# Background recurring TRIM worker for APFS hole punching / discard reclamation
(while true; do sleep 300; fstrim -a >/dev/null 2>&1; done) &

# Mount cgroups v2
mkdir -p /sys/fs/cgroup
mount -t cgroup2 cgroup2 /sys/fs/cgroup
echo "[AERO-INIT] cgroups v2 hierarchy active."

# Load virtualization, networking and translation kernel modules
modprobe virtio_net 2>/dev/null || true
modprobe virtio_balloon 2>/dev/null || true
modprobe virtiofs 2>/dev/null || true
modprobe vmw_vsock_virtio_transport 2>/dev/null || true
modprobe overlay 2>/dev/null || true
modprobe ip_tables 2>/dev/null || true
modprobe iptable_filter 2>/dev/null || true
modprobe iptable_nat 2>/dev/null || true
modprobe br_netfilter 2>/dev/null || true
modprobe binfmt_misc 2>/dev/null || true
sysctl -w net.ipv4.ip_forward=1 2>/dev/null || true
echo "[AERO-INIT] Virtualization, networking and vsock drivers loaded."

# Network setup
ip link set lo up
if ip link show eth0 >/dev/null 2>&1; then
    ip link set eth0 up
    echo "[AERO-INIT] Waiting for eth0 link carrier..."
    for i in $(seq 1 50); do
        CARRIER=$(cat /sys/class/net/eth0/carrier 2>/dev/null || echo "0")
        if [ "${CARRIER}" = "1" ] || ip link show eth0 | grep -q "state UP"; then
            echo "[AERO-INIT] eth0 carrier detected after ${i} ticks."
            break
        fi
        sleep 0.1
    done

    # Run udhcpc for dynamic lease negotiation
    echo "[AERO-INIT] Negotiating DHCP lease on eth0..."
    udhcpc -i eth0 -s /usr/share/udhcpc/default.script -n -t 5 -T 2 -q >/dev/kmsg 2>&1 || \
        udhcpc -i eth0 -b -s /usr/share/udhcpc/default.script >/dev/kmsg 2>&1 &

    # Detect dynamic gateway rather than hardcoding 192.168.64.1
    GATEWAY=$(ip route show default dev eth0 2>/dev/null | cut -d" " -f3 | head -n 1)
    if [ -z "${GATEWAY}" ]; then
        GATEWAY="192.168.64.1"
        ip addr add 192.168.64.2/24 dev eth0 2>/dev/null || true
        ip route add default via "${GATEWAY}" dev eth0 2>/dev/null || true
    fi

    # Add host.docker.internal pointing to the host gateway into guest /etc/hosts
    sed -i "/host\.docker\.internal/d" /etc/hosts 2>/dev/null || true
    echo "${GATEWAY} host.docker.internal" >> /etc/hosts

    # Configure DNS in /etc/resolv.conf
    cat << EOF_RESOLV > /etc/resolv.conf
nameserver ${GATEWAY}
nameserver 1.1.1.1
nameserver 8.8.8.8
EOF_RESOLV

    # Ensure NAT masquerading for outbound container traffic
    iptables -t nat -A POSTROUTING -o eth0 -j MASQUERADE 2>/dev/null || true

    echo "[AERO-INIT] Network eth0 configured (gateway: ${GATEWAY}, host.docker.internal mapped)."
fi

# VirtioFS workspace mounts
mkdir -p /workspace /mnt/mac
mount -t virtiofs orb_fs /workspace 2>/dev/null || mount -t virtiofs orb_fs /mnt/mac 2>/dev/null || true
echo "[AERO-INIT] VirtioFS workspace mounted at /workspace."

# Apple Silicon Rosetta 2 Linux Binary Translation
mkdir -p /run/rosetta /media/rosetta /proc/sys/fs/binfmt_misc
if mount -t virtiofs rosetta /run/rosetta 2>/dev/null || mount -t virtiofs rosetta /media/rosetta 2>/dev/null; then
    mountpoint -q /proc/sys/fs/binfmt_misc || mount -t binfmt_misc binfmt_misc /proc/sys/fs/binfmt_misc 2>/dev/null || true
    ROSETTA_BIN="/run/rosetta/rosetta"
    if [ ! -f "${ROSETTA_BIN}" ] && [ -f "/media/rosetta/rosetta" ]; then
        ROSETTA_BIN="/media/rosetta/rosetta"
    fi
    if [ -f "${ROSETTA_BIN}" ] && [ -w /proc/sys/fs/binfmt_misc/register ]; then
        if [ ! -f /proc/sys/fs/binfmt_misc/rosetta ]; then
            echo ":rosetta:M::\x7fELF\x02\x01\x01\x00\x00\x00\x00\x00\x00\x00\x00\x00\x02\x00\x3e\x00:\xff\xff\xff\xff\xff\xfe\xfe\x00\xff\xff\xff\xff\xff\xff\xff\xff\xfe\xff\xff\xff:${ROSETTA_BIN}:CF" > /proc/sys/fs/binfmt_misc/register 2>/dev/null || true
        fi
        echo "[AERO-INIT] Apple Silicon Rosetta 2 binfmt_misc registered for x86_64 Linux translation."
    fi
fi

# Start containerd
mkdir -p /var/run /var/log /var/lib/docker
containerd --config /etc/containerd/config.toml >/dev/kmsg 2>&1 &
echo "[AERO-INIT] containerd service active."

# Start dockerd
dockerd --debug --config-file /etc/docker/daemon.json >/dev/kmsg 2>&1 &
echo "[AERO-INIT] dockerd daemon launched with debug logging."

# Poll for dockerd Unix domain socket
for i in $(seq 1 100); do
    if [ -S /var/run/docker.sock ]; then
        echo "[AERO-INIT] Docker daemon socket ready: /var/run/docker.sock"
        break
    fi
    sleep 0.05
done

# Preload alpine container image into dockerd
if [ -f /var/lib/docker-preload/alpine.tar ]; then
    echo "[AERO-INIT] Preloading alpine image into dockerd..."
    docker load -i /var/lib/docker-preload/alpine.tar >/dev/kmsg 2>&1 &
fi

# Start AeroStack guest agent
if [ -f /usr/local/bin/orb-agent ]; then
    echo "[AERO-INIT] Starting AeroStack guest agent..."
    /usr/local/bin/orb-agent \
        -docker-sock /var/run/docker.sock \
        -mount-root /workspace \
        -mem-port 1024 \
        -fs-port 9922 \
        -exec-port 8022 \
        -docker-port 2375 >/dev/kmsg 2>&1 &
fi

echo "[AERO-INIT] MicroVM guest fully initialized and operational."
while true; do
    sleep 3600
done
EOF_INIT

chmod 755 /tmp/rootfs/sbin/init
ln -sf /sbin/init /tmp/rootfs/init

echo "  * Formatting and populating rootfs.ext4 (64GB sparse disk)..."
rm -f /output/rootfs.ext4
truncate -s 64G /output/rootfs.ext4
mke2fs -t ext4 -F -L "AERO_ROOT" -d /tmp/rootfs /output/rootfs.ext4 64G >/dev/null

echo "  * Asset compilation completed successfully inside container."
'

if [ -f "${CACHE_DIR}/rootfs.ext4" ] && [ "${CACHE_DIR}" != "${PROJECT_DIR}/aerostack_runtime/cache" ]; then
    mkdir -p "${PROJECT_DIR}/aerostack_runtime/cache"
    ln -sf "${CACHE_DIR}/rootfs.ext4" "${PROJECT_DIR}/aerostack_runtime/cache/rootfs.ext4"
    if [ -f "${CACHE_DIR}/vmlinux" ]; then
        ln -sf "${CACHE_DIR}/vmlinux" "${PROJECT_DIR}/aerostack_runtime/cache/vmlinux"
    fi
    if [ -f "${CACHE_DIR}/initramfs-virt" ]; then
        ln -sf "${CACHE_DIR}/initramfs-virt" "${PROJECT_DIR}/aerostack_runtime/cache/initramfs-virt"
    fi
fi

echo "=========================================================="
echo " Guest Assets Verified in ${CACHE_DIR}:"
ls -lh "${CACHE_DIR}"
echo "=========================================================="
