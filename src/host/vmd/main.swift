import Foundation
import Darwin
import OrbEngine

@main
struct OrbVMDMain {
    static func main() async {
        print("""
        ======================================================================
           ⚡ OrbStack Free - Production Host Engine Daemon (orb-vmd)
           Hypervisor: macOS Virtualization.framework (Darwin 23+ / Apple Silicon)
           Target: Low-tens MB Footprint | Zero Host Polling | VirtioFS Bridge
        ======================================================================
        """)

        // Parse arguments
        var socketPath = ("~/.orbstack/run/vmcontrol.sock" as NSString).expandingTildeInPath
        var dockerSocketPath = ("~/.orbstack/run/docker.sock" as NSString).expandingTildeInPath
        var watchDir = "/Volumes/AdamSSD/antigravity_workspace"

        let args = CommandLine.arguments
        for i in 0..<args.count {
            if args[i] == "--socket" && i + 1 < args.count {
                socketPath = (args[i + 1] as NSString).expandingTildeInPath
            } else if args[i] == "--docker-socket" && i + 1 < args.count {
                dockerSocketPath = (args[i + 1] as NSString).expandingTildeInPath
            } else if args[i] == "--watch" && i + 1 < args.count {
                watchDir = (args[i + 1] as NSString).expandingTildeInPath
            } else if args[i] == "--help" || args[i] == "-h" {
                print("""
                Usage: orb-vmd [options]
                  --socket <path>         Control socket path (default: ~/.orbstack/run/vmcontrol.sock)
                  --docker-socket <path>  Docker socket bridge path (default: ~/.orbstack/run/docker.sock)
                  --watch <path>          Host workspace directory to watch with FSEvents
                  --help, -h              Show this help message
                """)
                return
            }
        }

        print("[ENGINE] Initializing Core Subsystems...")

        // 1. VM Manager Spec
        let spec = VMSpec(
            name: "ubuntu",
            distro: "Ubuntu",
            version: "24.04",
            arch: "arm64",
            cpuCount: min(8, ProcessInfo.processInfo.activeProcessorCount),
            memoryBytes: 4 * 1024 * 1024 * 1024,
            shareConfigs: [
                VMShareConfig(hostURL: URL(fileURLWithPath: watchDir), tag: "orb_fs", isReadOnly: false)
            ]
        )
        let vmManager = VMManager(spec: spec)

        // 2. Dynamic Memory Balloon Controller
        let balloonController = MemoryBalloonController(initialTargetMB: 512)
        let footprint = MemoryBalloonController.queryHostFootprint()
        print(String(format: "[MEMORY] Initial Host Footprint: %.2f MB resident, %.2f MB phys_footprint", footprint.residentMB, footprint.footprintMB))

        // 3. Host File Watcher (FSEvents -> vsock 9922)
        let fileWatcher = HostFileWatcher(watchPaths: [watchDir])
        print("[FS] Configured FSEvents watcher on: \(watchDir)")

        // 4. Vsock Router
        let vsockRouter = VsockRouter()
        vsockRouter.memoryController = balloonController
        vsockRouter.fileWatcher = fileWatcher

        // 5. JSON-RPC 2.0 Server
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

        // Try binding Docker socket bridge
        do {
            try vsockRouter.startDockerSocketProxy(unixSocketPath: dockerSocketPath)
            print("[DOCKER] Docker socket bridge listening on: \(dockerSocketPath)")
        } catch {
            print("[DOCKER] Note: Docker bridge socket at \(dockerSocketPath) deferred: \(error.localizedDescription)")
        }

        // Signal handlers for graceful termination
        signal(SIGINT) { _ in
            print("\n[DAEMON] Caught SIGINT. Shutting down gracefully...")
            exit(0)
        }
        signal(SIGTERM) { _ in
            print("\n[DAEMON] Caught SIGTERM. Shutting down gracefully...")
            exit(0)
        }

        print("[ENGINE] Host daemon is running and ready. Press Ctrl+C to exit.")
        dispatchMain()
    }
}
