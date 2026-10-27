import Foundation
import Virtualization
import Darwin

public final class VsockRouter: NSObject, VZVirtioSocketListenerDelegate, @unchecked Sendable {
    public static let dockerPort: UInt32   = 2375
    public static let memoryPort: UInt32   = 1024
    public static let fsEventsPort: UInt32 = 9922
    public static let execPort: UInt32     = 8022

    public weak var socketDevice: VZVirtioSocketDevice?
    public weak var memoryController: MemoryBalloonController?
    public weak var fileWatcher: HostFileWatcher?

    private var listeners: [UInt32: VZVirtioSocketListener] = [:]
    private let routerQueue = DispatchQueue(label: "com.orbstack_free.vsock_router", qos: .userInteractive)
    private let routerLock = NSLock()
    private var isSimulated: Bool = false

    // Registered port handlers for custom streams (e.g. Docker, Exec)
    public var onDockerConnection: ((VZVirtioSocketConnection) -> Void)?
    public var onExecConnection: ((VZVirtioSocketConnection) -> Void)?

    // Telemetry handler for memory stream
    public var onTelemetryReceived: ((GuestTelemetryFrame) -> Void)?

    public init(socketDevice: VZVirtioSocketDevice? = nil) {
        self.socketDevice = socketDevice
        super.init()
    }

    /// Attach to an active VZVirtioSocketDevice and configure listeners
    public func attach(device: VZVirtioSocketDevice) {
        self.socketDevice = device
        registerListeners()
    }

    /// Register listeners on supported vsock ports
    public func registerListeners() {
        guard let device = socketDevice else { return }

        let ports = [Self.dockerPort, Self.memoryPort, Self.fsEventsPort, Self.execPort]
        for port in ports {
            let listener = VZVirtioSocketListener()
            listener.delegate = self
            device.setSocketListener(listener, forPort: port)
            routerLock.lock()
            listeners[port] = listener
            routerLock.unlock()
        }
    }

    // MARK: - VZVirtioSocketListenerDelegate

    public func listener(
        _ listener: VZVirtioSocketListener,
        shouldAcceptNewConnection connection: VZVirtioSocketConnection,
        from socketDevice: VZVirtioSocketDevice
    ) -> Bool {
        let destPort = connection.destinationPort

        switch destPort {
        case Self.memoryPort:
            handleMemoryConnection(connection)
            return true

        case Self.fsEventsPort:
            handleFsEventsConnection(connection)
            return true

        case Self.dockerPort:
            onDockerConnection?(connection)
            return true

        case Self.execPort:
            onExecConnection?(connection)
            return true

        default:
            return false
        }
    }

    // MARK: - Port Handlers

    private func handleMemoryConnection(_ connection: VZVirtioSocketConnection) {
        let fd = connection.fileDescriptor
        routerQueue.async { [weak self] in
            var buf = [UInt8](repeating: 0, count: 40)
            while true {
                var total = 0
                while total < 40 {
                    let r = buf.withUnsafeMutableBytes { raw in
                        Darwin.read(fd, raw.baseAddress!.advanced(by: total), 40 - total)
                    }
                    if r <= 0 { return }
                    total += r
                }

                let data = Data(buf)
                if let frame = GuestTelemetryFrame.decode(from: data) {
                    self?.memoryController?.handleTelemetry(telemetry: frame)
                    self?.onTelemetryReceived?(frame)
                }
            }
        }
    }

    private func handleFsEventsConnection(_ connection: VZVirtioSocketConnection) {
        let fd = connection.fileDescriptor
        let transport = SocketStreamTransport(fileDescriptor: fd)
        fileWatcher?.transport = transport
    }

    // MARK: - Outbound Guest Connections

    public func connectToGuest(port: UInt32) async throws -> VZVirtioSocketConnection {
        guard let device = socketDevice else {
            throw NSError(domain: "VsockRouter", code: -1, userInfo: [NSLocalizedDescriptionKey: "No active VZVirtioSocketDevice"])
        }

        return try await withCheckedThrowingContinuation { continuation in
            device.connect(toPort: port) { result in
                switch result {
                case .success(let connection):
                    continuation.resume(returning: connection)
                case .failure(let error):
                    continuation.resume(throwing: error)
                }
            }
        }
    }

    // MARK: - Docker Socket Bridge

    /// Bridges a local Unix domain socket directly to Guest Docker vsock port 2375
    public func startDockerSocketProxy(unixSocketPath: String) throws {
        let dir = (unixSocketPath as NSString).deletingLastPathComponent
        try FileManager.default.createDirectory(atPath: dir, withIntermediateDirectories: true)
        unlink(unixSocketPath)

        let serverFd = Darwin.socket(AF_UNIX, SOCK_STREAM, 0)
        guard serverFd >= 0 else {
            throw NSError(domain: "VsockRouter", code: -2, userInfo: [NSLocalizedDescriptionKey: "Failed to create unix socket"])
        }

        var addr = sockaddr_un()
        addr.sun_family = sa_family_t(AF_UNIX)
        let maxLen = MemoryLayout.size(ofValue: addr.sun_path)
        _ = unixSocketPath.withCString {
            strncpy(&addr.sun_path.0, $0, maxLen - 1)
        }

        let addrLen = socklen_t(MemoryLayout<sockaddr_un>.size)
        let bindRes = withUnsafePointer(to: &addr) {
            $0.withMemoryRebound(to: sockaddr.self, capacity: 1) {
                Darwin.bind(serverFd, $0, addrLen)
            }
        }
        guard bindRes == 0 else {
            close(serverFd)
            throw NSError(domain: "VsockRouter", code: -3, userInfo: [NSLocalizedDescriptionKey: "Failed to bind unix socket"])
        }

        Darwin.listen(serverFd, 128)
        chmod(unixSocketPath, 0o666)

        routerQueue.async { [weak self] in
            while true {
                var clientAddr = sockaddr_un()
                var clientLen = socklen_t(MemoryLayout<sockaddr_un>.size)
                let clientFd = withUnsafeMutablePointer(to: &clientAddr) {
                    $0.withMemoryRebound(to: sockaddr.self, capacity: 1) {
                        Darwin.accept(serverFd, $0, &clientLen)
                    }
                }
                guard clientFd >= 0 else { continue }

                Task {
                    guard let self = self else {
                        close(clientFd)
                        return
                    }
                    do {
                        let guestConn = try await self.connectToGuest(port: Self.dockerPort)
                        self.spliceSockets(clientFd: clientFd, guestFd: guestConn.fileDescriptor)
                    } catch {
                        close(clientFd)
                    }
                }
            }
        }
    }

    private func spliceSockets(clientFd: Int32, guestFd: Int32) {
        let q = DispatchQueue(label: "com.orbstack_free.splice.\(clientFd)", attributes: .concurrent)

        // Client -> Guest
        q.async {
            var buf = [UInt8](repeating: 0, count: 65536)
            while true {
                let n = Darwin.read(clientFd, &buf, buf.count)
                if n <= 0 { break }
                _ = Darwin.write(guestFd, &buf, n)
            }
            Darwin.close(clientFd)
            Darwin.close(guestFd)
        }

        // Guest -> Client
        q.async {
            var buf = [UInt8](repeating: 0, count: 65536)
            while true {
                let n = Darwin.read(guestFd, &buf, buf.count)
                if n <= 0 { break }
                _ = Darwin.write(clientFd, &buf, n)
            }
            Darwin.close(clientFd)
            Darwin.close(guestFd)
        }
    }
}
