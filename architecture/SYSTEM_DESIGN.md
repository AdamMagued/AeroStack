# orbstack_free: Comprehensive System Design Document

**Status:** Architecture Blueprint & Research Specification  
**Author:** Big Boss & Lead Systems Architect  
**Target Platform:** macOS 13+ (Darwin 23+), Apple Silicon (ARM64)  
**License:** Apache 2.0 / MIT Dual License  

---

## 1. Executive Summary & Core Mandate

`orbstack_free` is a clean-room, open-source container runtime and Linux virtual machine manager engineered specifically for macOS on Apple Silicon. It is architected to achieve complete functional parity with OrbStack while setting a new historical record for memory efficiency:

- **Idle Resident Memory:** Low tens of megabytes (target: 30 MB – 50 MB RSS on Darwin).
- **Idle CPU Utilization:** Strictly < 0.1% using true event-driven suspend/sleep loops.
- **Boot & Provisioning Latency:** < 1 second microVM startup; instant container execution.
- **Docker Engine API:** 100% drop-in socket compatibility at `/var/run/docker.sock` and `~/.orbstack/run/docker.sock`.
- **Linux MicroVMs:** Full multi-distro support (Alpine, Ubuntu, Debian, Arch, Fedora) with isolated namespaces and instant instantiation.
- **Filesystem Bridge:** High-throughput VirtioFS paired with a custom bidirectional event synchronizer (`FSEvents` <-> `inotify`), eliminating file polling overhead for web dev tools.
- **Networking:** Unprivileged and bridge-backed dual-mode networking with direct container IP access and automatic local domain resolution.

---

## 2. Architectural Blueprint

```
+-----------------------------------------------------------------------------------+
|                                    macOS Host                                     |
|                                                                                   |
|  +--------------------+   +-----------------------+   +------------------------+  |
|  |   orb / orbctl     |   |   Docker CLI/Compose  |   |    Native SwiftUI GUI  |  |
|  |     (Host CLI)     |   |    (Standard Tools)   |   |    (Monitor & Config)  |  |
|  +---------+----------+   +-----------+-----------+   +-----------+------------+  |
|            |                          |                           |               |
|            v                          v                           v               |
|  +-----------------------------------------------------------------------------+  |
|  |                     orb-host-daemon (Host Control Plane)                    |  |
|  |                                                                             |  |
|  |  +-----------------------+  +-----------------------+  +-----------------+  |  |
|  |  |  Docker Socket Proxy  |  | Memory Balloon Engine |  |  FSEvents Sync  |  |  |
|  |  |  & Path Rewriter      |  | Dynamic Hysteresis    |  |  Watcher Engine |  |  |
|  |  +-----------------------+  +-----------------------+  +-----------------+  |  |
|  |  +-----------------------------------------------------------------------+  |  |
|  |  |                 Virtualization.framework Hypervisor Core             |  |  |
|  |  |    VZVirtualMachine, VZVirtioSocket, VZVirtioFS, VZVirtioBalloon      |  |  |
|  |  +-----------------------------------------------------------------------+  |  |
|  +------------------------------------+----------------------------------------+  |
+---------------------------------------|-------------------------------------------+
                                        | VirtIO Bus (vsock, virtio-fs, virtio-net)
+---------------------------------------v-------------------------------------------+
|                               Linux MicroVM Guest                                 |
|                                                                                   |
|  +-----------------------------------------------------------------------------+  |
|  |                       orb-guest-agent (Linux Control Plane)                 |  |
|  |                                                                             |  |
|  |  +-----------------------+  +-----------------------+  +-----------------+  |  |
|  |  |  Memory PSI Monitor   |  | inotify Event Injector|  | vsock Multiplex |  |  |
|  |  |  & Auto-Compactor     |  | Live File Watcher     |  | Stream Bridge   |  |  |
|  |  +-----------------------+  +-----------------------+  +-----------------+  |  |
|  +------------------------------------+----------------------------------------+  |
|                                       |                                           |
|            +--------------------------+---------------------------+               |
|            v                                                      v               |
|  +-------------------------+                           +-----------------------+  |
|  |  Docker / containerd    |                           |  Linux Distro Subsys  |  |
|  |  (Containers & Pods)    |                           |  (Ubuntu/Debian/etc.) |  |
|  +-------------------------+                           +-----------------------+  |
+-----------------------------------------------------------------------------------+
```

---

## 3. Subsystem Deep-Dive

### 3.1. Ultra-Extreme Memory Subsystem (Dynamic Ballooning & Darwin Reclamation)

#### The Problem with Traditional Container Engines
Traditional engines (Docker Desktop, Podman Desktop, Colima) statically allocate 4 GB – 8 GB of host memory. Even when idle, macOS wired memory or swap holds gigabytes of unutilized guest memory.

#### The `orbstack_free` Solution
1. **Dynamic Elastic Ceiling:** The VM is configured with a high ceiling (e.g. 8 GB – 16 GB), but `VZVirtioTraditionalMemoryBalloonDevice` actively inflates the balloon to squeeze idle guest memory down to the bare minimum needed by running processes.
2. **Darwin Mach VM Page Reclamation:** When `targetVirtualMachineMemorySize` decreases, the Linux guest `virtio_balloon` driver grabs unused physical pages from the Linux buddy allocator. The hypervisor unmaps these pages and yields them back to the host via Darwin `madvise(MADV_DONTNEED)` and `mach_vm_deallocate`.
3. **Guest Pressure Stall Information (PSI) Loop:**
   - The `orb-guest-agent` continuously inspects `/proc/pressure/memory` and `/proc/meminfo`.
   - When memory pressure is low (idle state), the agent triggers guest compaction (`echo 1 > /proc/sys/vm/compact_memory`) and signals the host balloon manager.
   - When workload spikes occur (container starting, compiler running), the agent deflates the balloon instantly (or lets `VIRTIO_BALLOON_F_DEFLATE_ON_OOM` reclaim memory within microseconds).
4. **Result:** Idle host RSS is kept in the low tens of megabytes (~30 MB to 50 MB).

### 3.2. High-Throughput VirtioFS Filesystem Bridge & Event Synchronizer

#### The File Watching Dilemma
VirtioFS provides near-native read/write bandwidth on Apple Silicon NVMe, but Linux `inotify` does not automatically receive notifications when files are edited on the macOS host. As a consequence, web development frameworks (`vite`, `next`, `webpack`, `nodemon`) are forced to use CPU-burning polling loops (`CHOKIDAR_USEPOLLING=true`).

#### The `orbstack_free` Solution
1. **Host-Side FSEvents Stream:** `HostFileWatcher` monitors shared directory roots using macOS `FSEventStreamCreate`.
2. **Binary Event Multiplexing:** Events are encoded as compact binary frames:
   ```c
   struct FileEventFrame {
       uint8_t  event_type; // CREATE, MODIFY, DELETE, RENAME
       uint16_t path_len;
       char     path[path_len];
   };
   ```
3. **Guest-Side inotify Injection:**
   - `orb-guest-agent` receives the frame over a dedicated vsock port.
   - It performs a low-overhead atomic `utimensat` or touch on the target VirtioFS inode.
   - This fires a native inotify event in the Linux guest kernel, notifying any active watcher in < 2ms without polling.

### 3.3. Docker Engine API Socket Bridge & Transparent Path Rewriter

#### Socket Flow
1. macOS host listens on `/var/run/docker.sock` and `~/.orbstack/run/docker.sock`.
2. Client HTTP requests are proxied across `VZVirtioSocketConnection` (vsock port 2375) directly to the guest `dockerd` socket.
3. Hijacked streaming connections (`docker exec -it`, `docker build`, `docker logs -f`) are spliced directly between file descriptors using zero-copy transfers.

#### Path Rewriting
When a developer runs `docker run -v $(pwd):/app ...`:
1. The host socket proxy inspects the container creation JSON payload (`HostConfig.Binds` and `Mounts`).
2. Host paths (e.g. `/Users/username/workspace` or `/Volumes/AdamSSD/workspace`) are dynamically mapped to their corresponding VirtioFS guest mounts (e.g. `/mnt/mac/Users/...` or `/workspace`).
3. The rewritten JSON is forwarded to `dockerd`, maintaining compatibility with standard macOS directory paths.

### 3.4. Dual-Mode Bi-Directional Networking & Local DNS

1. **Native Bridge Mode (Default):**
   - Uses `VZVmnetNetworkDeviceAttachment` or `vmnet.framework` in shared mode.
   - Allocates a dedicated subnet (`192.168.138.0/23`).
   - Every container and VM is directly routable from macOS. Developers can run `curl http://192.168.138.10:3000` directly from macOS terminal or browser.
2. **Unprivileged Mode (Fallback):**
   - Uses `VZFileHandleNetworkDeviceAttachment` with raw Ethernet datagram frames routed to a high-speed userspace network stack (`smoltcp`).
   - Operates 100% without root permissions or special privileges.
3. **Local Domain Resolution:**
   - Embedded DNS responder handles `.orb.local` and `.local` domain queries.
   - Integrates with macOS via `/etc/resolver/orb.local`.
   - Running containers are automatically assigned DNS names based on container name and project (e.g. `api.myproject.orb.local`).

### 3.5. Multi-Distro Linux VM Subsystem

1. **Distros Supported:** Alpine, Ubuntu, Debian, Arch, Fedora, Rocky, CentOS.
2. **Storage Architecture:**
   - Base rootfs images stored in compressed, read-only template stores.
   - Instant instantiation via guest Btrfs subvolume snapshots or OverlayFS (`lowerdir=/templates/ubuntu, upperdir=/vms/ubuntu_dev/upper`).
   - Startup time: sub-500ms from command invocation to root shell.
3. **CLI Interface:** Drop-in parity with `orbctl`:
   - `orbctl create [distro] [name]`
   - `orbctl list`
   - `orbctl run [name] [command]`
   - `orbctl stop [name]`, `orbctl delete [name]`

---

## 4. Verification & Testing Strategy

- **Automated Memory Test Harness:** Measures host RSS at idle, under container burst, and post-container teardown to verify that memory drops back to low tens of MBs.
- **File Watching Latency Harness:** Emits a host write and measures the microsecond timestamp of Linux inotify reception.
- **Docker Engine API Compliance Suite:** Executes Docker CLI compliance commands (pull, run, port forward, volume mount, compose up/down).
- **Network Routing Test:** Pings and curls container IPs and verifies DNS resolution across host and guest.
