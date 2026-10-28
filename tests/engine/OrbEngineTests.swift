import XCTest
import Virtualization
import Darwin
@testable import OrbEngine

final class OrbEngineTests: XCTestCase {

    // MARK: - 1. VMManager Tests

    func testVMManagerConfigurationValidation() throws {
        let tempDir = URL(fileURLWithPath: NSTemporaryDirectory()).appendingPathComponent("orb_test_\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: tempDir, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: tempDir) }

        let dummyDisk = tempDir.appendingPathComponent("disk.raw")
        FileManager.default.createFile(atPath: dummyDisk.path, contents: Data(count: 1024 * 1024))

        let spec = VMSpec(
            name: "test-vm",
            distro: "Ubuntu",
            version: "24.04",
            arch: "arm64",
            cpuCount: 2,
            memoryBytes: 2 * 1024 * 1024 * 1024,
            diskConfigs: [VMDiskConfig(imageURL: dummyDisk)],
            shareConfigs: [VMShareConfig(hostURL: tempDir, tag: "test_share")]
        )

        let manager = VMManager(spec: spec)
        let config = try manager.buildConfiguration(networkMode: .nat, validate: false)

        XCTAssertEqual(config.cpuCount, 2)
        XCTAssertEqual(config.memorySize, 2 * 1024 * 1024 * 1024)
        XCTAssertEqual(config.storageDevices.count, 1)
        XCTAssertGreaterThanOrEqual(config.directorySharingDevices.count, 1)
        XCTAssertEqual(config.networkDevices.count, 1)
        XCTAssertEqual(config.socketDevices.count, 1)
        XCTAssertEqual(config.memoryBalloonDevices.count, 1)
        XCTAssertEqual(config.entropyDevices.count, 1)

        // Verify summary
        let summary = manager.summary
        XCTAssertEqual(summary.name, "test-vm")
        XCTAssertEqual(summary.state, "STOPPED")
        XCTAssertEqual(summary.cpus, 2)
    }

    func testVMManagerFileHandleNetworkAttachment() throws {
        var fds: [Int32] = [0, 0]
        XCTAssertEqual(socketpair(AF_UNIX, SOCK_DGRAM, 0, &fds), 0)
        let handle = FileHandle(fileDescriptor: fds[0], closeOnDealloc: true)
        defer { close(fds[1]) }

        let spec = VMSpec(name: "net-vm")
        let manager = VMManager(spec: spec)
        let config = try manager.buildConfiguration(networkMode: .fileHandle(handle), validate: false)

        XCTAssertEqual(config.networkDevices.count, 1)
        let netDev = config.networkDevices[0]
        XCTAssertTrue(netDev.attachment is VZFileHandleNetworkDeviceAttachment)
    }

    // MARK: - 2. MemoryBalloonController Tests

    final class MockBalloonDevice: MemoryBalloonDeviceProtocol {
        var targetVirtualMachineMemorySize: UInt64 = 512 * 1024 * 1024
    }

    func testMemoryTelemetryFrameCodecs() {
        let frame = GuestTelemetryFrame(
            magic: VSOCK_MEM_MAGIC,
            msgType: MemMessageType.telemetryReport.rawValue,
            totalRamBytes: 4096 * 1024 * 1024,
            freeRamBytes: 1024 * 1024 * 1024,
            availRamBytes: 2048 * 1024 * 1024,
            psiSomeAvg10: 250, // 2.50%
            psiFullAvg10: 100  // 1.00%
        )

        var data = Data(capacity: 40)
        var m = frame.magic
        var t = frame.msgType
        var res = frame.reserved
        var tot = frame.totalRamBytes
        var fr = frame.freeRamBytes
        var av = frame.availRamBytes
        var ps = frame.psiSomeAvg10
        var pf = frame.psiFullAvg10

        data.append(Data(bytes: &m, count: 4))
        data.append(Data(bytes: &t, count: 1))
        data.append(Data(bytes: &res, count: 3))
        data.append(Data(bytes: &tot, count: 8))
        data.append(Data(bytes: &fr, count: 8))
        data.append(Data(bytes: &av, count: 8))
        data.append(Data(bytes: &ps, count: 4))
        data.append(Data(bytes: &pf, count: 4))

        XCTAssertEqual(data.count, 40)

        let decoded = GuestTelemetryFrame.decode(from: data)
        XCTAssertNotNil(decoded)
        XCTAssertEqual(decoded?.magic, VSOCK_MEM_MAGIC)
        XCTAssertEqual(decoded?.totalRamBytes, 4096 * 1024 * 1024)
        XCTAssertEqual(decoded?.availRamBytes, 2048 * 1024 * 1024)
        XCTAssertEqual(decoded?.psiSomeAvg10, 250)
        XCTAssertEqual(decoded?.psiFullAvg10, 100)
    }

    func testHostControlFrameEncoding() {
        let control = HostControlFrame(
            magic: VSOCK_MEM_MAGIC,
            msgType: MemMessageType.compactCommand.rawValue,
            flags: 0x3,
            reserved: 0,
            targetRamBytes: 64 * 1024 * 1024
        )

        let data = control.encode()
        XCTAssertEqual(data.count, 16)

        data.withUnsafeBytes { raw in
            XCTAssertEqual(raw.load(fromByteOffset: 0, as: UInt32.self), VSOCK_MEM_MAGIC)
            XCTAssertEqual(raw.load(fromByteOffset: 4, as: UInt8.self), MemMessageType.compactCommand.rawValue)
            XCTAssertEqual(raw.load(fromByteOffset: 5, as: UInt8.self), 0x3)
            XCTAssertEqual(raw.load(fromByteOffset: 8, as: UInt64.self), 64 * 1024 * 1024)
        }
    }

    func testMemoryBalloonBurstOnPSIStall() {
        let mockDevice = MockBalloonDevice()
        mockDevice.targetVirtualMachineMemorySize = 512 * 1024 * 1024

        let controller = MemoryBalloonController(balloonDevice: mockDevice, initialTargetMB: 512)
        controller.burstStepMB = 256

        let exp = expectation(description: "Burst target adjusted")
        controller.sendControlFrameHandler = { frame in
            if frame.msgType == MemMessageType.setTargetAck.rawValue {
                exp.fulfill()
            }
        }

        // Send telemetry with high PSI stall (6.5%)
        let telemetry = GuestTelemetryFrame(
            magic: VSOCK_MEM_MAGIC,
            msgType: MemMessageType.telemetryReport.rawValue,
            totalRamBytes: 4096 * 1024 * 1024,
            freeRamBytes: 30 * 1024 * 1024,
            availRamBytes: 40 * 1024 * 1024, // below 50MB watermark
            psiSomeAvg10: 650,               // 6.50% stall
            psiFullAvg10: 200
        )

        controller.handleTelemetry(telemetry: telemetry)
        wait(for: [exp], timeout: 2.0)

        XCTAssertGreaterThan(mockDevice.targetVirtualMachineMemorySize, 512 * 1024 * 1024)
        XCTAssertEqual(controller.idleTicks, 0)
    }

    func testMemoryBalloonSlowCompactionAndReclaim() {
        let mockDevice = MockBalloonDevice()
        mockDevice.targetVirtualMachineMemorySize = 512 * 1024 * 1024

        let controller = MemoryBalloonController(balloonDevice: mockDevice, initialTargetMB: 512)
        controller.idleHoldThresholdTicks = 2
        controller.reclaimStepMB = 64
        controller.targetIdleReserveMB = 40

        var compactionFired = false
        var stepDownFired = false
        let exp = expectation(description: "Compaction and Reclaim fired")
        exp.expectedFulfillmentCount = 2

        controller.sendControlFrameHandler = { frame in
            if frame.msgType == MemMessageType.compactCommand.rawValue {
                compactionFired = true
                exp.fulfill()
            } else if frame.msgType == MemMessageType.setTargetAck.rawValue && compactionFired {
                stepDownFired = true
                exp.fulfill()
            }
        }

        // Telemetry with lots of free/available RAM and 0% PSI
        let idleTelemetry = GuestTelemetryFrame(
            magic: VSOCK_MEM_MAGIC,
            msgType: MemMessageType.telemetryReport.rawValue,
            totalRamBytes: 4096 * 1024 * 1024,
            freeRamBytes: 300 * 1024 * 1024,
            availRamBytes: 350 * 1024 * 1024, // > 40 + 64
            psiSomeAvg10: 0,
            psiFullAvg10: 0
        )

        // Tick 1
        controller.handleTelemetry(telemetry: idleTelemetry)
        // Tick 2 (triggers compaction)
        controller.handleTelemetry(telemetry: idleTelemetry)
        // Tick 3 (triggers step-down)
        controller.handleTelemetry(telemetry: idleTelemetry)

        wait(for: [exp], timeout: 2.0)
        XCTAssertTrue(compactionFired)
        XCTAssertTrue(stepDownFired)
        XCTAssertLessThan(mockDevice.targetVirtualMachineMemorySize, 512 * 1024 * 1024)
    }

    func testDarwinHostFootprintQuery() {
        let footprint = MemoryBalloonController.queryHostFootprint()
        XCTAssertGreaterThan(footprint.residentMB, 0.0)
        XCTAssertGreaterThan(footprint.footprintMB, 0.0)
        // Idle Darwin footprint should be well bounded
        print(String(format: "[TEST AUDIT] Darwin task_vm_info footprint: %.2f MB resident, %.2f MB physical footprint", footprint.residentMB, footprint.footprintMB))
    }

    // MARK: - 3. HostFileWatcher Tests

    func testFileEventFrameCodecs() {
        let original = FileEventFrame(
            opcode: ORB_OP_EVENT,
            inotifyMask: ORB_IN_CREATE | ORB_IN_MODIFY,
            seq: 42,
            timestampNs: 1_700_000_000_000_000_000,
            relativePath: "src/app/index.ts"
        )

        let encoded = original.encode()
        XCTAssertGreaterThanOrEqual(encoded.count, 32 + "src/app/index.ts".utf8.count)

        let decoded = FileEventFrame.decode(from: encoded)
        XCTAssertNotNil(decoded)
        XCTAssertEqual(decoded?.magic, ORB_FS_MAGIC)
        XCTAssertEqual(decoded?.version, ORB_FS_VERSION)
        XCTAssertEqual(decoded?.opcode, ORB_OP_EVENT)
        XCTAssertEqual(decoded?.inotifyMask, ORB_IN_CREATE | ORB_IN_MODIFY)
        XCTAssertEqual(decoded?.seq, 42)
        XCTAssertEqual(decoded?.relativePath, "src/app/index.ts")
    }

    func testFSEventsToInotifyMapping() {
        let createFlags = FSEventStreamEventFlags(kFSEventStreamEventFlagItemCreated)
        XCTAssertEqual(HostFileWatcher.mapFSEventsToInotify(createFlags) & ORB_IN_CREATE, ORB_IN_CREATE)

        let removeFlags = FSEventStreamEventFlags(kFSEventStreamEventFlagItemRemoved)
        XCTAssertEqual(HostFileWatcher.mapFSEventsToInotify(removeFlags) & ORB_IN_DELETE, ORB_IN_DELETE)

        let modFlags = FSEventStreamEventFlags(kFSEventStreamEventFlagItemModified)
        XCTAssertNotEqual(HostFileWatcher.mapFSEventsToInotify(modFlags) & ORB_IN_MODIFY, 0)
    }

    final class MockTransport: EventStreamTransport, @unchecked Sendable {
        var writtenData: [Data] = []
        let lock = NSLock()

        func write(data: Data) -> Int {
            lock.lock()
            writtenData.append(data)
            lock.unlock()
            return data.count
        }

        func read(into buffer: inout [UInt8], maxCount: Int) -> Int {
            // Emulate EOF
            usleep(10000)
            return 0
        }
    }

    func testHostFileWatcherDispatchAndStats() {
        let mockTransport = MockTransport()
        let watcher = HostFileWatcher(watchPaths: ["/tmp"], transport: mockTransport)

        let exp = expectation(description: "Event dispatched")
        watcher.onEventDispatched = { frame in
            if frame.relativePath == "test.txt" {
                exp.fulfill()
            }
        }

        watcher.dispatchEvent(relativePath: "test.txt", inotifyMask: ORB_IN_MODIFY)
        wait(for: [exp], timeout: 1.0)

        let stats = watcher.getStats()
        XCTAssertEqual(stats.events_sent, 1)
        XCTAssertGreaterThan(stats.bytes_sent, 32)
        XCTAssertEqual(mockTransport.writtenData.count, 1)
    }

    // MARK: - 4. RPCServer Tests

    func testRPCServerJSONRPCDirection() throws {
        let tempSock = URL(fileURLWithPath: NSTemporaryDirectory()).appendingPathComponent("test_\(UUID().uuidString).sock").path
        let server = RPCServer(socketPath: tempSock)
        try server.start()
        defer { server.stop() }

        // Test vm.list
        let listReq = RPCRequest(method: "vm.list", id: .int(1))
        let listResp = server.dispatch(request: listReq)
        XCTAssertNil(listResp.error)
        XCTAssertNotNil(listResp.result)

        // Test vm.start
        let startReq = RPCRequest(method: "vm.start", params: ["name": .string("ubuntu")], id: .int(2))
        let startResp = server.dispatch(request: startReq)
        XCTAssertNil(startResp.error)
        XCTAssertEqual(startResp.result, .bool(true))

        // Test metrics.get
        let metricsReq = RPCRequest(method: "metrics.get", id: .int(3))
        let metricsResp = server.dispatch(request: metricsReq)
        XCTAssertNil(metricsResp.error)

        // Test unknown method
        let badReq = RPCRequest(method: "nonexistent.method", id: .int(4))
        let badResp = server.dispatch(request: badReq)
        XCTAssertNotNil(badResp.error)
        XCTAssertEqual(badResp.error?.code, RPCError.methodNotFound.code)
    }

    func testRPCServerMachineLifecycleAndDoctor() throws {
        let tempSock = URL(fileURLWithPath: NSTemporaryDirectory()).appendingPathComponent("test_\(UUID().uuidString).sock").path
        let tempMachDir = URL(fileURLWithPath: NSTemporaryDirectory()).appendingPathComponent("orb_rpc_test_\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: tempMachDir, withIntermediateDirectories: true)
        defer {
            unlink(tempSock)
            try? FileManager.default.removeItem(at: tempMachDir)
        }

        let server = RPCServer(socketPath: tempSock, machinesDirectory: tempMachDir)
        try server.start()
        defer { server.stop() }

        // 1. Test vm.create
        let createReq = RPCRequest(method: "vm.create", params: [
            "name": .string("test-box"),
            "distro": .string("alpine:3.20"),
            "arch": .string("arm64"),
            "cpus": .int(2),
            "memory": .string("1G")
        ], id: .int(10))
        let createResp = server.dispatch(request: createReq)
        XCTAssertNil(createResp.error)
        XCTAssertEqual(createResp.result, .bool(true))

        let boxDir = tempMachDir.appendingPathComponent("test-box")
        let boxMeta = boxDir.appendingPathComponent("machine.json")
        XCTAssertTrue(FileManager.default.fileExists(atPath: boxMeta.path))

        // 2. Test vm.clone
        let cloneReq = RPCRequest(method: "vm.clone", params: [
            "source": .string("test-box"),
            "destination": .string("test-box-cloned")
        ], id: .int(11))
        let cloneResp = server.dispatch(request: cloneReq)
        XCTAssertNil(cloneResp.error)
        XCTAssertEqual(cloneResp.result, .bool(true))

        let cloneDir = tempMachDir.appendingPathComponent("test-box-cloned")
        let cloneMeta = cloneDir.appendingPathComponent("machine.json")
        XCTAssertTrue(FileManager.default.fileExists(atPath: cloneMeta.path))

        // 3. Test vm.list
        let listReq = RPCRequest(method: "vm.list", id: .int(12))
        let listResp = server.dispatch(request: listReq)
        XCTAssertNil(listResp.error)

        // 4. Test vm.delete
        let delReq = RPCRequest(method: "vm.delete", params: [
            "name": .string("test-box")
        ], id: .int(13))
        let delResp = server.dispatch(request: delReq)
        XCTAssertNil(delResp.error)
        XCTAssertEqual(delResp.result, .bool(true))
        XCTAssertFalse(FileManager.default.fileExists(atPath: boxDir.path))

        // 5. Test doctor.run
        let doctorReq = RPCRequest(method: "doctor.run", id: .int(14))
        let doctorResp = server.dispatch(request: doctorReq)
        XCTAssertNil(doctorResp.error)
        XCTAssertNotNil(doctorResp.result)
    }

    func testRPCServerUnixDomainSocketClient() throws {
        let tempSock = URL(fileURLWithPath: NSTemporaryDirectory()).appendingPathComponent("test_sock_\(UUID().uuidString).sock").path
        let server = RPCServer(socketPath: tempSock)
        try server.start()
        defer { server.stop() }

        // Connect client
        let fd = Darwin.socket(AF_UNIX, SOCK_STREAM, 0)
        XCTAssertGreaterThanOrEqual(fd, 0)
        defer { Darwin.close(fd) }

        var addr = sockaddr_un()
        addr.sun_family = sa_family_t(AF_UNIX)
        _ = tempSock.withCString {
            strncpy(&addr.sun_path.0, $0, MemoryLayout.size(ofValue: addr.sun_path) - 1)
        }

        let res = withUnsafePointer(to: &addr) {
            $0.withMemoryRebound(to: sockaddr.self, capacity: 1) {
                Darwin.connect(fd, $0, socklen_t(MemoryLayout<sockaddr_un>.size))
            }
        }
        XCTAssertEqual(res, 0, "Socket connection failed")

        // Send line-delimited request
        let req = RPCRequest(method: "vm.list", id: .int(99))
        var data = try JSONEncoder().encode(req)
        data.append(UInt8(ascii: "\n"))

        let w = data.withUnsafeBytes { raw in
            Darwin.write(fd, raw.baseAddress, raw.count)
        }
        XCTAssertEqual(w, data.count)

        // Read response
        var readBuf = [UInt8](repeating: 0, count: 8192)
        let r = Darwin.read(fd, &readBuf, readBuf.count)
        XCTAssertGreaterThan(r, 0)

        let respData = Data(readBuf[0..<r])
        let resp = try JSONDecoder().decode(RPCResponse.self, from: respData)
        XCTAssertEqual(resp.id, .int(99))
        XCTAssertNil(resp.error)
    }

    // MARK: - 5. VsockRouter Port Constants & Exec Bridge

    func testVsockRouterPortAssignments() {
        XCTAssertEqual(VsockRouter.dockerPort, 2375)
        XCTAssertEqual(VsockRouter.memoryPort, 1024)
        XCTAssertEqual(VsockRouter.fsEventsPort, 9922)
        XCTAssertEqual(VsockRouter.execPort, 8022)
    }

    func testVsockRouterExecBridge() throws {
        let router = VsockRouter()
        let tempSock = URL(fileURLWithPath: NSTemporaryDirectory()).appendingPathComponent("test_exec_\(UUID().uuidString).sock").path
        let testPort: UInt16 = 18022

        try router.startExecBridge(tcpPort: testPort, unixSocketPath: tempSock)

        // Connect TCP client to 127.0.0.1:testPort
        let tcpFd = Darwin.socket(AF_INET, SOCK_STREAM, 0)
        XCTAssertGreaterThanOrEqual(tcpFd, 0)
        defer { Darwin.close(tcpFd) }

        var inAddr = sockaddr_in()
        inAddr.sin_family = sa_family_t(AF_INET)
        inAddr.sin_port = testPort.bigEndian
        inet_pton(AF_INET, "127.0.0.1", &inAddr.sin_addr)

        let connRes = withUnsafePointer(to: &inAddr) {
            $0.withMemoryRebound(to: sockaddr.self, capacity: 1) {
                Darwin.connect(tcpFd, $0, socklen_t(MemoryLayout<sockaddr_in>.size))
            }
        }
        XCTAssertEqual(connRes, 0, "Failed to connect to exec bridge TCP port")

        // Also check unix socket exists
        XCTAssertTrue(FileManager.default.fileExists(atPath: tempSock))
    }
}
