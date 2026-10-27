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
    public var vmQueue: DispatchQueue?

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
    public func attach(device: VZVirtioSocketDevice, queue: DispatchQueue? = nil) {
        self.socketDevice = device
        if let q = queue {
            self.vmQueue = q
        }
        if let q = self.vmQueue {
            q.async { [weak self] in
                self?.registerListeners()
            }
        } else {
            registerListeners()
        }
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

    public func connectToGuest(port: UInt32, retries: Int = 15, delaySeconds: Double = 0.2) async throws -> VZVirtioSocketConnection {
        guard let device = socketDevice else {
            throw NSError(domain: "VsockRouter", code: -1, userInfo: [NSLocalizedDescriptionKey: "No active VZVirtioSocketDevice"])
        }
        let queue = self.vmQueue ?? DispatchQueue.main

        var lastError: Error?
        for attempt in 1...max(1, retries) {
            do {
                return try await withCheckedThrowingContinuation { continuation in
                    queue.async {
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
            } catch {
                lastError = error
                if attempt < retries {
                    try await Task.sleep(nanoseconds: UInt64(delaySeconds * 1_000_000_000))
                }
            }
        }
        throw lastError ?? NSError(domain: "VsockRouter", code: -1, userInfo: [NSLocalizedDescriptionKey: "Failed to connect to guest port \(port) after \(retries) attempts"])
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
                print("[DOCKER] Incoming connection accepted on clientFd: \(clientFd)")

                Task {
                    guard let self = self else {
                        close(clientFd)
                        return
                    }
                    do {
                        let guestConn = try await self.connectToGuest(port: Self.dockerPort)
                        print("[DOCKER] Connected to guest vsock 2375, guestFd: \(guestConn.fileDescriptor)")
                        self.spliceSockets(clientFd: clientFd, guestConn: guestConn)
                    } catch {
                        print("[DOCKER] Connection to guest Docker vsock (port \(Self.dockerPort)) failed: \(error)")
                        close(clientFd)
                    }
                }
            }
        }
    }

    private func spliceSockets(clientFd: Int32, guestConn: VZVirtioSocketConnection) {
        let guestFd = guestConn.fileDescriptor
        var optVal: Int32 = 1
        _ = setsockopt(clientFd, SOL_SOCKET, SO_NOSIGPIPE, &optVal, socklen_t(MemoryLayout<Int32>.size))
        _ = setsockopt(guestFd, SOL_SOCKET, SO_NOSIGPIPE, &optVal, socklen_t(MemoryLayout<Int32>.size))

        let q = DispatchQueue(label: "com.orbstack_free.splice.\(clientFd)", attributes: .concurrent)
        let closeLock = NSLock()
        var completedCount = 0
        var isClosed = false
        var connectionHolder: VZVirtioSocketConnection? = guestConn

        let terminate = {
            closeLock.lock()
            defer { closeLock.unlock() }
            if !isClosed {
                isClosed = true
                Darwin.shutdown(clientFd, SHUT_RDWR)
                Darwin.shutdown(guestFd, SHUT_RDWR)
                Darwin.close(clientFd)
                Darwin.close(guestFd)
                _ = connectionHolder?.fileDescriptor
                connectionHolder = nil
            }
        }

        let markDone = {
            closeLock.lock()
            completedCount += 1
            let allDone = (completedCount >= 2)
            closeLock.unlock()
            if allDone {
                terminate()
            }
        }

        // Client -> Guest (macOS Docker client -> Linux guest dockerd)
        q.async {
            let bufferSize = 65536
            let buf = UnsafeMutablePointer<UInt8>.allocate(capacity: bufferSize)
            defer { buf.deallocate() }
            while true {
                let n = Darwin.read(clientFd, buf, bufferSize)
                if n <= 0 {
                    break
                }
                var written = 0
                var writeFailed = false
                while written < n {
                    let w = Darwin.write(guestFd, buf.advanced(by: written), n - written)
                    if w <= 0 {
                        writeFailed = true
                        break
                    }
                    written += w
                }
                if writeFailed {
                    terminate()
                    return
                }
            }
            Darwin.shutdown(guestFd, SHUT_WR)
            markDone()
        }

        // Guest -> Client (Linux guest dockerd -> macOS Docker client)
        q.async {
            let bufferSize = 65536
            let buf = UnsafeMutablePointer<UInt8>.allocate(capacity: bufferSize)
            defer { buf.deallocate() }
            while true {
                let n = Darwin.read(guestFd, buf, bufferSize)
                if n <= 0 {
                    break
                }
                var written = 0
                var writeFailed = false
                while written < n {
                    let w = Darwin.write(clientFd, buf.advanced(by: written), n - written)
                    if w <= 0 {
                        writeFailed = true
                        break
                    }
                    written += w
                }
                if writeFailed {
                    terminate()
                    return
                }
            }
            Darwin.shutdown(clientFd, SHUT_WR)
            markDone()
        }
    }
}
