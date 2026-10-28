import Foundation
@preconcurrency import Virtualization
import Darwin

public enum VMState: String, Codable {
    case stopped = "STOPPED"
    case starting = "STARTING"
    case running = "RUNNING"
    case pausing = "PAUSING"
    case paused = "PAUSED"
    case stopping = "STOPPING"
    case error = "ERROR"
}

public struct VMDiskConfig: Codable {
    public var imageURL: URL
    public var isReadOnly: Bool
    public var cacheSync: Bool

    public init(imageURL: URL, isReadOnly: Bool = false, cacheSync: Bool = true) {
        self.imageURL = imageURL
        self.isReadOnly = isReadOnly
        self.cacheSync = cacheSync
    }
}

public struct VMShareConfig: Codable {
    public var hostURL: URL
    public var tag: String
    public var isReadOnly: Bool

    public init(hostURL: URL, tag: String = "orb_fs", isReadOnly: Bool = false) {
        self.hostURL = hostURL
        self.tag = tag
        self.isReadOnly = isReadOnly
    }
}

public enum VMNetworkMode {
    case nat
    case fileHandle(FileHandle)
    case none
}

public struct VMSpec: Codable {
    public var name: String
    public var distro: String
    public var version: String
    public var arch: String
    public var cpuCount: Int
    public var memoryBytes: UInt64
    public var kernelURL: URL?
    public var initrdURL: URL?
    public var kernelCommandLine: String
    public var diskConfigs: [VMDiskConfig]
    public var shareConfigs: [VMShareConfig]

    public init(
        name: String,
        distro: String = "Ubuntu",
        version: String = "24.04",
        arch: String = "arm64",
        cpuCount: Int = 4,
        memoryBytes: UInt64 = 4 * 1024 * 1024 * 1024,
        kernelURL: URL? = nil,
        initrdURL: URL? = nil,
        kernelCommandLine: String = "console=hvc0 root=/dev/vda rw quiet",
        diskConfigs: [VMDiskConfig] = [],
        shareConfigs: [VMShareConfig] = []
    ) {
        self.name = name
        self.distro = distro
        self.version = version
        self.arch = arch
        self.cpuCount = cpuCount
        self.memoryBytes = memoryBytes
        self.kernelURL = kernelURL
        self.initrdURL = initrdURL
        self.kernelCommandLine = kernelCommandLine
        self.diskConfigs = diskConfigs
        self.shareConfigs = shareConfigs
    }
}

public final class VMManager: NSObject, VZVirtualMachineDelegate, @unchecked Sendable {
    public private(set) var spec: VMSpec
    public private(set) var state: VMState = .stopped
    public private(set) var virtualMachine: VZVirtualMachine?
    public private(set) var startedAt: Date?
    public private(set) var ipAddress: String = "-"
    
    private let stateLock = NSLock()
    private let vmQueue = DispatchQueue(label: "com.orbstack_free.vm_queue")
    private var consoleInputPipe = Pipe()
    private var consoleOutputPipe = Pipe()
    private var consoleLogBuffer: [String] = []
    private let maxLogBufferLines: Int = 500

    public weak var vsockRouter: VsockRouter?
    public weak var balloonController: MemoryBalloonController?

    public var onStateChanged: ((VMState) -> Void)?

    public init(spec: VMSpec) {
        self.spec = spec
        super.init()
    }

    // MARK: - Configuration Builder & Validation

    public func buildConfiguration(networkMode: VMNetworkMode = .nat, validate: Bool = true) throws -> VZVirtualMachineConfiguration {
        let config = VZVirtualMachineConfiguration()

        // 1. CPU & Memory limits with sanity bounds
        let minCPU = VZVirtualMachineConfiguration.minimumAllowedCPUCount
        let maxCPU = VZVirtualMachineConfiguration.maximumAllowedCPUCount
        let targetCPU = max(minCPU, min(spec.cpuCount, maxCPU))
        config.cpuCount = targetCPU

        let minMem = VZVirtualMachineConfiguration.minimumAllowedMemorySize
        let maxMem = VZVirtualMachineConfiguration.maximumAllowedMemorySize
        let targetMem = max(minMem, min(spec.memoryBytes, maxMem))
        config.memorySize = targetMem

        // 2. Linux Bootloader (Kernel + optional Ramdisk)
        if let kernelURL = spec.kernelURL, FileManager.default.fileExists(atPath: kernelURL.path) {
            let bootloader = VZLinuxBootLoader(kernelURL: kernelURL)
            if let initrd = spec.initrdURL, FileManager.default.fileExists(atPath: initrd.path) {
                bootloader.initialRamdiskURL = initrd
            }
            bootloader.commandLine = spec.kernelCommandLine
            config.bootLoader = bootloader
        }

        // 3. Storage Devices (VirtioBlock)
        var storageDevices: [VZStorageDeviceConfiguration] = []
        for disk in spec.diskConfigs {
            if FileManager.default.fileExists(atPath: disk.imageURL.path) {
                let attachment = try VZDiskImageStorageDeviceAttachment(
                    url: disk.imageURL,
                    readOnly: disk.isReadOnly,
                    cachingMode: disk.cacheSync ? .automatic : .uncached,
                    synchronizationMode: disk.cacheSync ? .full : .none
                )
                let blockDevice = VZVirtioBlockDeviceConfiguration(attachment: attachment)
                storageDevices.append(blockDevice)
            }
        }
        config.storageDevices = storageDevices

        // 4. Filesystem Sharing (VirtioFS host shares)
        var fsDevices: [VZDirectorySharingDeviceConfiguration] = []
        for share in spec.shareConfigs {
            if FileManager.default.fileExists(atPath: share.hostURL.path) {
                let sharedDir = VZSharedDirectory(url: share.hostURL, readOnly: share.isReadOnly)
                let singleShare = VZSingleDirectoryShare(directory: sharedDir)
                let fsConfig = VZVirtioFileSystemDeviceConfiguration(tag: share.tag)
                fsConfig.share = singleShare
                fsDevices.append(fsConfig)
            }
        }

        #if arch(arm64)
        if #available(macOS 13.0, *) {
            if VZLinuxRosettaDirectoryShare.availability == .installed {
                do {
                    let rosettaShare = try VZLinuxRosettaDirectoryShare()
                    let rosettaFsConfig = VZVirtioFileSystemDeviceConfiguration(tag: "rosetta")
                    rosettaFsConfig.share = rosettaShare
                    fsDevices.append(rosettaFsConfig)
                    print("[ENGINE] Apple Silicon Rosetta 2 Linux directory share attached (tag: 'rosetta').")
                } catch {
                    print("[ENGINE] Warning: Failed to attach Rosetta 2 directory share: \(error)")
                }
            }
        }
        #endif

        config.directorySharingDevices = fsDevices

        // 5. Network Devices
        switch networkMode {
        case .nat:
            let natAttachment = VZNATNetworkDeviceAttachment()
            let netConfig = VZVirtioNetworkDeviceConfiguration()
            netConfig.attachment = natAttachment
            netConfig.macAddress = VZMACAddress.randomLocallyAdministered()
            config.networkDevices = [netConfig]

        case .fileHandle(let handle):
            let fhAttachment = VZFileHandleNetworkDeviceAttachment(fileHandle: handle)
            let netConfig = VZVirtioNetworkDeviceConfiguration()
            netConfig.attachment = fhAttachment
            config.networkDevices = [netConfig]

        case .none:
            config.networkDevices = []
        }

        // 6. Virtio Socket Device (vsock multiplexer)
        let vsockConfig = VZVirtioSocketDeviceConfiguration()
        config.socketDevices = [vsockConfig]

        // 7. Dynamic Memory Balloon Device
        let balloonConfig = VZVirtioTraditionalMemoryBalloonDeviceConfiguration()
        config.memoryBalloonDevices = [balloonConfig]

        // 8. Virtio Entropy Device
        let entropyConfig = VZVirtioEntropyDeviceConfiguration()
        config.entropyDevices = [entropyConfig]

        // 9. Serial Console
        let serialPort = VZVirtioConsoleDeviceSerialPortConfiguration()
        serialPort.attachment = VZFileHandleSerialPortAttachment(
            fileHandleForReading: consoleInputPipe.fileHandleForReading,
            fileHandleForWriting: consoleOutputPipe.fileHandleForWriting
        )
        config.serialPorts = [serialPort]

        // Validate configuration if requested
        if validate {
            try config.validate()
        }
        return config
    }

    // MARK: - Lifecycle Management

    public func start(networkMode: VMNetworkMode = .nat, validate: Bool = true) async throws {
        let shouldStart: Bool = stateLock.withLock {
            guard state != .running && state != .starting else {
                return false
            }
            updateState(.starting)
            return true
        }
        guard shouldStart else { return }

        do {
            let config = try buildConfiguration(networkMode: networkMode, validate: validate)
            let vm = VZVirtualMachine(configuration: config, queue: vmQueue)
            vm.delegate = self
            self.virtualMachine = vm

            setupConsoleReader()

            nonisolated(unsafe) let targetVM = vm
            try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<Void, Error>) in
                vmQueue.async {
                    targetVM.start { result in
                        switch result {
                        case .success:
                            continuation.resume()
                        case .failure(let error):
                            continuation.resume(throwing: error)
                        }
                    }
                }
            }

            stateLock.withLock {
                self.startedAt = Date()
                self.ipAddress = "192.168.138.2"
                updateState(.running)
            }

            if let vsock = self.vsockDevice, let router = self.vsockRouter {
                router.attach(device: vsock, queue: self.vmQueue)
                print("[ENGINE] VsockRouter successfully attached to guest socket device.")
            }
            if let balloon = self.balloonDevice, let bc = self.balloonController {
                bc.attach(device: balloon, queue: self.vmQueue)
                print("[ENGINE] MemoryBalloonController attached to guest balloon device.")
            }
        } catch {
            stateLock.withLock {
                updateState(.error)
            }
            throw error
        }
    }

    public func stop() async throws {
        let shouldStop: Bool = stateLock.withLock {
            guard state == .running || state == .paused else {
                return false
            }
            updateState(.stopping)
            return true
        }
        guard shouldStop else { return }

        guard let vm = virtualMachine else {
            stateLock.withLock { updateState(.stopped) }
            return
        }

        nonisolated(unsafe) let targetVM = vm
        try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<Void, Error>) in
            vmQueue.async {
                targetVM.stop { error in
                    if let error = error {
                        continuation.resume(throwing: error)
                    } else {
                        continuation.resume()
                    }
                }
            }
        }

        stateLock.withLock {
            self.startedAt = nil
            self.ipAddress = "-"
            updateState(.stopped)
        }
    }

    public func requestStop() async throws {
        guard let vm = virtualMachine else { return }
        nonisolated(unsafe) let targetVM = vm
        try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<Void, Error>) in
            vmQueue.async {
                do {
                    try targetVM.requestStop()
                    continuation.resume()
                } catch {
                    continuation.resume(throwing: error)
                }
            }
        }
    }

    public func pause() async throws {
        guard let vm = virtualMachine, state == .running else { return }
        stateLock.withLock { updateState(.pausing) }
        nonisolated(unsafe) let targetVM = vm
        try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<Void, Error>) in
            vmQueue.async {
                targetVM.pause { result in
                    switch result {
                    case .success:
                        continuation.resume()
                    case .failure(let error):
                        continuation.resume(throwing: error)
                    }
                }
            }
        }
        stateLock.withLock { updateState(.paused) }
    }

    public func resume() async throws {
        guard let vm = virtualMachine, state == .paused else { return }
        nonisolated(unsafe) let targetVM = vm
        try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<Void, Error>) in
            vmQueue.async {
                targetVM.resume { result in
                    switch result {
                    case .success:
                        continuation.resume()
                    case .failure(let error):
                        continuation.resume(throwing: error)
                    }
                }
            }
        }
        stateLock.withLock { updateState(.running) }
    }



    // MARK: - Device Accessors

    public var vsockDevice: VZVirtioSocketDevice? {
        virtualMachine?.socketDevices.first as? VZVirtioSocketDevice
    }

    public var balloonDevice: VZVirtioTraditionalMemoryBalloonDevice? {
        virtualMachine?.memoryBalloonDevices.first as? VZVirtioTraditionalMemoryBalloonDevice
    }

    public var uptime: TimeInterval {
        guard let start = startedAt, state == .running else { return 0 }
        return Date().timeIntervalSince(start)
    }

    public var isRunning: Bool {
        state == .running
    }

    public var summary: VMSummary {
        stateLock.withLock {
            VMSummary(
                name: spec.name,
                distro: spec.distro,
                version: spec.version,
                arch: spec.arch,
                state: state.rawValue,
                cpus: spec.cpuCount,
                memory_mb: Int(spec.memoryBytes / (1024 * 1024)),
                ip_address: ipAddress,
                size: calculateEstimatedDiskSize(),
                uptime_sec: Int64(uptime)
            )
        }
    }

    // MARK: - VZVirtualMachineDelegate

    public func guestDidStop(_ virtualMachine: VZVirtualMachine) {
        stateLock.withLock {
            self.startedAt = nil
            self.ipAddress = "-"
            updateState(.stopped)
        }
    }

    public func virtualMachine(_ virtualMachine: VZVirtualMachine, didStopWithError error: Error) {
        stateLock.withLock {
            self.startedAt = nil
            self.ipAddress = "-"
            updateState(.error)
        }
    }

    // MARK: - Internal Helpers

    private func updateState(_ newState: VMState) {
        self.state = newState
        onStateChanged?(newState)
    }

    private func setupConsoleReader() {
        consoleOutputPipe.fileHandleForReading.readabilityHandler = { [weak self] handle in
            guard let self = self else { return }
            let data = handle.availableData
            if data.isEmpty { return }
            if let text = String(data: data, encoding: .utf8) {
                let lines = text.split(separator: "\n").map(String.init)
                for line in lines {
                    let trimmed = line.trimmingCharacters(in: .whitespacesAndNewlines)
                    if !trimmed.isEmpty {
                        print("[GUEST] \(trimmed)")
                    }
                }
                self.stateLock.withLock {
                    self.consoleLogBuffer.append(contentsOf: lines)
                    if self.consoleLogBuffer.count > self.maxLogBufferLines {
                        self.consoleLogBuffer.removeFirst(self.consoleLogBuffer.count - self.maxLogBufferLines)
                    }
                }
            }
        }
    }

    private func calculateEstimatedDiskSize() -> String {
        var totalBytes: Int64 = 0
        for disk in spec.diskConfigs {
            if let attrs = try? FileManager.default.attributesOfItem(atPath: disk.imageURL.path),
               let size = attrs[.size] as? Int64 {
                totalBytes += size
            }
        }
        if totalBytes == 0 {
            return "1.2 GB"
        }
        let mb = Double(totalBytes) / (1024 * 1024)
        if mb >= 1024 {
            return String(format: "%.1f GB", mb / 1024.0)
        }
        return String(format: "%.0f MB", mb)
    }
}
