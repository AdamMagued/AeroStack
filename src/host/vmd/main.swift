import Foundation
import Darwin
import AppKit
import OrbEngine

@main
struct OrbVMDMain {
    static func main() async {
        setlinebuf(stdout)
        setlinebuf(stderr)
        print("""
        ======================================================================
           AeroStack / OrbStack Free - Production Host Engine Daemon (orb-vmd)
           Hypervisor: macOS Virtualization.framework (Darwin 23+ / Apple Silicon)
           Target: Low-tens MB Footprint | Zero Host Polling | VirtioFS Bridge
        ======================================================================
        """)

        // Resolve runtime paths
        let projectRoot = resolveProjectRoot()
        var cacheDir = ("~/.aerostack/cache" as NSString).expandingTildeInPath
        var socketPath = ("~/.aerostack/run/vmcontrol.sock" as NSString).expandingTildeInPath
        var dockerSocketPath = ("~/.aerostack/run/docker.sock" as NSString).expandingTildeInPath
        var watchDir = projectRoot

        // Fallback to internal external workspace if default symlink not resolved
        let ssdDefaultCache = "\(projectRoot)/aerostack_runtime/cache"
        if !FileManager.default.fileExists(atPath: cacheDir) && FileManager.default.fileExists(atPath: ssdDefaultCache) {
            cacheDir = ssdDefaultCache
        }

        var customKernel: String? = nil
        var customInitrd: String? = nil
        var customRootfs: String? = nil
        var cpus = min(8, ProcessInfo.processInfo.activeProcessorCount)
        var memoryMB: UInt64 = 4096
        var shouldBoot = true

        let args = CommandLine.arguments
        for i in 0..<args.count {
            if args[i] == "--cache-dir" && i + 1 < args.count {
                cacheDir = (args[i + 1] as NSString).expandingTildeInPath
            } else if args[i] == "--socket" && i + 1 < args.count {
                socketPath = (args[i + 1] as NSString).expandingTildeInPath
            } else if args[i] == "--docker-socket" && i + 1 < args.count {
                dockerSocketPath = (args[i + 1] as NSString).expandingTildeInPath
            } else if args[i] == "--watch" && i + 1 < args.count {
                watchDir = (args[i + 1] as NSString).expandingTildeInPath
            } else if args[i] == "--kernel" && i + 1 < args.count {
                customKernel = (args[i + 1] as NSString).expandingTildeInPath
            } else if args[i] == "--initrd" && i + 1 < args.count {
                customInitrd = (args[i + 1] as NSString).expandingTildeInPath
            } else if args[i] == "--rootfs" && i + 1 < args.count {
                customRootfs = (args[i + 1] as NSString).expandingTildeInPath
            } else if args[i] == "--cpus" && i + 1 < args.count {
                if let c = Int(args[i + 1]) { cpus = c }
            } else if args[i] == "--memory" && i + 1 < args.count {
                if let m = UInt64(args[i + 1]) { memoryMB = m }
            } else if args[i] == "--no-boot" {
                shouldBoot = false
            } else if args[i] == "--help" || args[i] == "-h" {
                print("""
                Usage: orb-vmd [options]
                  --cache-dir <path>      Cache directory for guest assets (default: ~/.aerostack/cache)
                  --socket <path>         Control socket path (default: ~/.aerostack/run/vmcontrol.sock)
                  --docker-socket <path>  Docker socket bridge path (default: ~/.aerostack/run/docker.sock)
                  --watch <path>          Host workspace directory to watch with FSEvents
                  --kernel <path>         Custom vmlinux ARM64 kernel image
                  --initrd <path>         Custom initramfs image
                  --rootfs <path>         Custom rootfs ext4 image
                  --cpus <n>              Allocated vCPU core count (default: host cores up to 8)
                  --memory <mb>           Allocated RAM ceiling in MB (default: 4096)
                  --no-boot               Initialize daemon without immediately booting VM
                  --help, -h              Show this help message
                """)
                return
            }
        }

        print("[ENGINE] Cache directory: \(cacheDir)")
        print("[ENGINE] Docker socket: \(dockerSocketPath)")
        print("[ENGINE] Workspace VirtioFS share: \(watchDir)")

        // 1. Locate or automatically build guest assets
        let kernelPath = customKernel ?? "\(cacheDir)/vmlinux"
        let initrdPath = customInitrd ?? "\(cacheDir)/initramfs-virt"
        let rootfsPath = customRootfs ?? "\(cacheDir)/rootfs.ext4"

        let fm = FileManager.default
        if !fm.fileExists(atPath: kernelPath) || !fm.fileExists(atPath: initrdPath) || !fm.fileExists(atPath: rootfsPath) {
            print("[ENGINE] Required guest assets missing from cache. Triggering automated build pipeline...")
            let builderScript = "\(projectRoot)/scripts/build_guest_assets.sh"
            if fm.fileExists(atPath: builderScript) {
                let process = Process()
                process.executableURL = URL(fileURLWithPath: "/bin/bash")
                process.arguments = [builderScript]
                var env = ProcessInfo.processInfo.environment
                env["AEROSTACK_CACHE_DIR"] = cacheDir
                process.environment = env
                do {
                    try process.run()
                    process.waitUntilExit()
                    if process.terminationStatus != 0 {
                        print("[ENGINE] ERROR: Guest asset builder exited with code \(process.terminationStatus)")
                        exit(1)
                    }
                } catch {
                    print("[ENGINE] ERROR: Failed to execute asset builder: \(error)")
                    exit(1)
                }
            } else {
                print("[ENGINE] ERROR: Asset builder script not found at: \(builderScript)")
                exit(1)
            }
        }

        print("[ENGINE] Verified guest assets:")
        print("  Kernel:   \(kernelPath) (\(fileSize(atPath: kernelPath)))")
        print("  Initrd:   \(initrdPath) (\(fileSize(atPath: initrdPath)))")
        print("  Rootfs:   \(rootfsPath) (\(fileSize(atPath: rootfsPath)))")

        // 2. VM Manager Spec
        let spec = VMSpec(
            name: "aerostack",
            distro: "Alpine",
            version: "3.24",
            arch: "arm64",
            cpuCount: cpus,
            memoryBytes: memoryMB * 1024 * 1024,
            kernelURL: URL(fileURLWithPath: kernelPath),
            initrdURL: URL(fileURLWithPath: initrdPath),
            kernelCommandLine: "console=hvc0 root=/dev/vda rw rootflags=rw,discard rootfstype=ext4 init=/sbin/init",
            diskConfigs: [
                VMDiskConfig(imageURL: URL(fileURLWithPath: rootfsPath), isReadOnly: false, cacheSync: true)
            ],
            shareConfigs: [
                VMShareConfig(hostURL: URL(fileURLWithPath: "/Users"), tag: "mac_users", isReadOnly: false),
                VMShareConfig(hostURL: URL(fileURLWithPath: "/Volumes"), tag: "mac_volumes", isReadOnly: false),
                VMShareConfig(hostURL: URL(fileURLWithPath: watchDir), tag: "orb_fs", isReadOnly: false)
            ]
        )
        let vmManager = VMManager(spec: spec)

        // 3. Dynamic Memory Balloon Controller with Darwin Memory Pressure
        let balloonController = MemoryBalloonController(initialTargetMB: 512)
        balloonController.startMonitoringMemoryPressure()
        let footprint = MemoryBalloonController.queryHostFootprint()
        print(String(format: "[MEMORY] Initial Host Footprint: %.2f MB resident, %.2f MB phys_footprint", footprint.residentMB, footprint.footprintMB))

        // 4. Host File Watcher (FSEvents -> vsock 9922)
        let fileWatcher = HostFileWatcher(watchPaths: [watchDir])
        print("[FS] Configured FSEvents watcher on: \(watchDir)")

        // 5. Vsock Router
        let vsockRouter = VsockRouter()
        vsockRouter.memoryController = balloonController
        vsockRouter.fileWatcher = fileWatcher

        // Wire routers to VM Manager
        vmManager.vsockRouter = vsockRouter
        vmManager.balloonController = balloonController

        // 6. JSON-RPC 2.0 Server
        let rpcServer = RPCServer(socketPath: socketPath)
        rpcServer.vmManager = vmManager
        rpcServer.memoryController = balloonController
        rpcServer.fileWatcher = fileWatcher
        rpcServer.vsockRouter = vsockRouter

        do {
            try rpcServer.start()
            print("[RPC] Control server listening on unix domain socket: \(socketPath)")
        } catch {
            print("[RPC] ERROR: Failed to bind control socket: \(error)")
            exit(1)
        }

        // 7. Cold Boot Execution
        if shouldBoot {
            print("[ENGINE] Launching MicroVM cold-boot sequence...")
            let startTime = Date()
            do {
                try await vmManager.start(networkMode: .nat, validate: true)
                let elapsed = Date().timeIntervalSince(startTime)
                print(String(format: "[ENGINE] MicroVM successfully booted in %.3f seconds!", elapsed))
            } catch {
                print("[ENGINE] FATAL: Failed to boot MicroVM: \(error)")
                exit(1)
            }
        }

        // 8. Bind Docker vsock backend & Exec bridge
        let backendDockerSocket = ("~/.aerostack/run/docker_backend.sock" as NSString).expandingTildeInPath
        do {
            try vsockRouter.startDockerSocketProxy(unixSocketPath: backendDockerSocket)
            print("[DOCKER] Raw Docker vsock backend listening on: \(backendDockerSocket)")
        } catch {
            print("[DOCKER] ERROR: Failed to bind Docker vsock backend: \(error)")
        }

        let execSocketPath = ("~/.aerostack/run/exec.sock" as NSString).expandingTildeInPath
        do {
            try vsockRouter.startExecBridge(tcpPort: 8022, unixSocketPath: execSocketPath)
            print("[EXEC] Host vsock exec bridge listening on 127.0.0.1:8022 and \(execSocketPath)")
        } catch {
            print("[EXEC] ERROR: Failed to bind exec bridge: \(error)")
        }

        // 9. Spawn managed helper daemons (orb-docker-bridge and orb-netd)
        if let bridgeBin = resolveBinaryPath(name: "orb-docker-bridge") {
            let bridgeArgs = [
                "--backend-mode", "unix",
                "--backend-socket", backendDockerSocket,
                "--sockets", "\(dockerSocketPath),/var/run/docker.sock",
                "--mount-prefix", "/"
            ]
            if let p = spawnSubprocess(binaryPath: bridgeBin, arguments: bridgeArgs) {
                print("[ENGINE] Spawned orb-docker-bridge (PID: \(p.processIdentifier))")
            }
        } else {
            do {
                try vsockRouter.startDockerSocketProxy(unixSocketPath: dockerSocketPath)
                print("[DOCKER] Fallback: Direct Docker socket bridge listening on: \(dockerSocketPath)")
            } catch {
                print("[DOCKER] Warning: Direct Docker bridge failed: \(error)")
            }
        }

        if let netdBin = resolveBinaryPath(name: "orb-netd") {
            let netdArgs = [
                "--docker-sock", dockerSocketPath,
                "--guest-ip", "192.168.64.2"
            ]
            if let p = spawnSubprocess(binaryPath: netdBin, arguments: netdArgs) {
                print("[ENGINE] Spawned orb-netd (PID: \(p.processIdentifier))")
            }
        }

        // 10. macOS Wake Notification Observer for Guest Clock Synchronization
        NSWorkspace.shared.notificationCenter.addObserver(
            forName: NSWorkspace.didWakeNotification,
            object: nil,
            queue: .main
        ) { _ in
            print("[ENGINE] macOS wake detected (NSWorkspace.didWakeNotification). Triggering guest clock sync...")
            Task {
                await Self.resyncGuestClock(vsockRouter: vsockRouter)
            }
        }

        // Signal handlers for graceful termination
        signal(SIGINT) { _ in
            print("\n[DAEMON] Caught SIGINT. Shutting down gracefully...")
            for p in OrbVMDMain.childProcesses {
                p.terminate()
            }
            exit(0)
        }
        signal(SIGTERM) { _ in
            print("\n[DAEMON] Caught SIGTERM. Shutting down gracefully...")
            for p in OrbVMDMain.childProcesses {
                p.terminate()
            }
            exit(0)
        }

        print("[ENGINE] AeroStack host engine daemon is RUNNING and ready.")
        while true {
            try? await Task.sleep(nanoseconds: 3600 * 1_000_000_000)
        }
    }

    private static func resyncGuestClock(vsockRouter: VsockRouter) async {
        do {
            let conn = try await vsockRouter.connectToGuest(port: VsockRouter.execPort, retries: 3, delaySeconds: 0.5)
            let fd = conn.fileDescriptor
            defer { Darwin.close(fd) }

            let req: [String: Any] = [
                "command": ["hwclock", "-s"],
                "machine": "default"
            ]
            let json = try JSONSerialization.data(withJSONObject: req)
            var packet = Data()
            packet.append(UInt8(1)) // ExecMsgRequest = 1
            var len = UInt32(json.count).bigEndian
            packet.append(Data(bytes: &len, count: 4))
            packet.append(json)

            _ = packet.withUnsafeBytes { raw in
                Darwin.write(fd, raw.baseAddress, raw.count)
            }
            print("[ENGINE] Guest clock resynchronization command ('hwclock -s') dispatched successfully.")
        } catch {
            print("[ENGINE] Warning: Could not resync guest clock: \(error)")
        }
    }

    nonisolated(unsafe) static var childProcesses: [Process] = []

    public static func resolveProjectRoot() -> String {
        if let env = ProcessInfo.processInfo.environment["AEROSTACK_ROOT"], FileManager.default.fileExists(atPath: env) {
            return env
        }
        let execURL = URL(fileURLWithPath: CommandLine.arguments[0]).resolvingSymlinksInPath()
        let candidate1 = execURL.deletingLastPathComponent().deletingLastPathComponent()
        if FileManager.default.fileExists(atPath: candidate1.appendingPathComponent("Package.swift").path) {
            return candidate1.path
        }
        let cwd = FileManager.default.currentDirectoryPath
        if FileManager.default.fileExists(atPath: (cwd as NSString).appendingPathComponent("Package.swift")) {
            return cwd
        }
        let ssdPath = "/Volumes/AdamSSD/antigravity_workspace/orbstack_free"
        if FileManager.default.fileExists(atPath: ssdPath) {
            return ssdPath
        }
        return ("~/.aerostack" as NSString).expandingTildeInPath
    }

    private static func resolveBinaryPath(name: String) -> String? {
        let fm = FileManager.default
        let root = resolveProjectRoot()
        let candidates = [
            "\(root)/build/\(name)",
            Bundle.main.bundleURL.deletingLastPathComponent().appendingPathComponent(name).path,
            "/usr/local/bin/\(name)"
        ]
        for c in candidates {
            if fm.fileExists(atPath: c) {
                return c
            }
        }
        return nil
    }

    private static func spawnSubprocess(binaryPath: String, arguments: [String]) -> Process? {
        let p = Process()
        p.executableURL = URL(fileURLWithPath: binaryPath)
        p.arguments = arguments
        p.environment = ProcessInfo.processInfo.environment
        do {
            try p.run()
            childProcesses.append(p)
            return p
        } catch {
            print("[ENGINE] Warning: Failed to spawn \(binaryPath): \(error)")
            return nil
        }
    }

    private static func fileSize(atPath path: String) -> String {
        guard let attrs = try? FileManager.default.attributesOfItem(atPath: path),
              let size = attrs[.size] as? Int64 else {
            return "unknown"
        }
        if size >= 1024 * 1024 * 1024 {
            return String(format: "%.1f GB", Double(size) / (1024.0 * 1024.0 * 1024.0))
        } else if size >= 1024 * 1024 {
            return String(format: "%.1f MB", Double(size) / (1024.0 * 1024.0))
        } else {
            return "\(size) bytes"
        }
    }
}
