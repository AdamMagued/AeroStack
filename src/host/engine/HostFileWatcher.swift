import Foundation
import CoreServices
import Darwin

public let ORB_FS_MAGIC: UInt32 = 0x4F524246 // "ORBF"
public let ORB_FS_VERSION: UInt16 = 1

public let ORB_OP_EVENT: UInt16  = 1
public let ORB_OP_PING: UInt16   = 2
public let ORB_OP_PONG: UInt16   = 3
public let ORB_OP_RESCAN: UInt16 = 4
public let ORB_OP_ACK: UInt16    = 5

public let ORB_IN_ACCESS: UInt32        = 0x00000001
public let ORB_IN_MODIFY: UInt32        = 0x00000002
public let ORB_IN_ATTRIB: UInt32        = 0x00000004
public let ORB_IN_CLOSE_WRITE: UInt32   = 0x00000008
public let ORB_IN_CLOSE_NOWRITE: UInt32 = 0x00000010
public let ORB_IN_OPEN: UInt32          = 0x00000020
public let ORB_IN_MOVED_FROM: UInt32    = 0x00000040
public let ORB_IN_MOVED_TO: UInt32      = 0x00000080
public let ORB_IN_CREATE: UInt32        = 0x00000100
public let ORB_IN_DELETE: UInt32        = 0x00000200
public let ORB_IN_DELETE_SELF: UInt32   = 0x00000400
public let ORB_IN_MOVE_SELF: UInt32     = 0x00000800
public let ORB_IN_ISDIR: UInt32         = 0x40000000

public struct FileEventFrame: Sendable {
    public var magic: UInt32
    public var version: UInt16
    public var opcode: UInt16
    public var inotifyMask: UInt32
    public var seq: UInt64
    public var timestampNs: UInt64
    public var relativePath: String

    public init(
        opcode: UInt16 = ORB_OP_EVENT,
        inotifyMask: UInt32,
        seq: UInt64,
        timestampNs: UInt64,
        relativePath: String
    ) {
        self.magic = ORB_FS_MAGIC
        self.version = ORB_FS_VERSION
        self.opcode = opcode
        self.inotifyMask = inotifyMask
        self.seq = seq
        self.timestampNs = timestampNs
        self.relativePath = relativePath
    }

    public func encode() -> Data {
        var data = Data(capacity: 32 + relativePath.utf8.count)
        var m = magic
        var v = version
        var op = opcode
        var mask = inotifyMask
        var s = seq
        var ts = timestampNs
        let pathBytes = Array(relativePath.utf8)
        var plen = UInt32(pathBytes.count)

        data.append(Data(bytes: &m, count: 4))
        data.append(Data(bytes: &v, count: 2))
        data.append(Data(bytes: &op, count: 2))
        data.append(Data(bytes: &mask, count: 4))
        data.append(Data(bytes: &s, count: 8))
        data.append(Data(bytes: &ts, count: 8))
        data.append(Data(bytes: &plen, count: 4))
        data.append(contentsOf: pathBytes)
        return data
    }

    public static func decode(from data: Data) -> FileEventFrame? {
        guard data.count >= 32 else { return nil }
        return data.withUnsafeBytes { raw in
            let magic = raw.loadUnaligned(fromByteOffset: 0, as: UInt32.self)
            guard magic == ORB_FS_MAGIC else { return nil }
            let version = raw.loadUnaligned(fromByteOffset: 4, as: UInt16.self)
            let opcode = raw.loadUnaligned(fromByteOffset: 6, as: UInt16.self)
            let mask = raw.loadUnaligned(fromByteOffset: 8, as: UInt32.self)
            let seq = raw.loadUnaligned(fromByteOffset: 12, as: UInt64.self)
            let ts = raw.loadUnaligned(fromByteOffset: 20, as: UInt64.self)
            let plen = Int(raw.loadUnaligned(fromByteOffset: 28, as: UInt32.self))

            guard data.count >= 32 + plen else { return nil }
            let pathData = data.subdata(in: 32..<(32 + plen))
            let path = String(data: pathData, encoding: .utf8) ?? ""
            var frame = FileEventFrame(
                opcode: opcode,
                inotifyMask: mask,
                seq: seq,
                timestampNs: ts,
                relativePath: path
            )
            frame.version = version
            return frame
        }
    }
}

public protocol EventStreamTransport: AnyObject, Sendable {
    func write(data: Data) -> Int
    func read(into buffer: inout [UInt8], maxCount: Int) -> Int
}

public final class SocketStreamTransport: EventStreamTransport, @unchecked Sendable {
    private let fd: Int32
    public init(fileDescriptor: Int32) {
        self.fd = fileDescriptor
    }

    public func write(data: Data) -> Int {
        data.withUnsafeBytes { raw in
            Darwin.write(fd, raw.baseAddress, raw.count)
        }
    }

    public func read(into buffer: inout [UInt8], maxCount: Int) -> Int {
        buffer.withUnsafeMutableBytes { raw in
            Darwin.read(fd, raw.baseAddress, maxCount)
        }
    }
}

private final class FSEventContextBox: @unchecked Sendable {
    weak var watcher: HostFileWatcher?
    let watchPrefix: String

    init(watcher: HostFileWatcher, watchPrefix: String) {
        self.watcher = watcher
        self.watchPrefix = watchPrefix
    }
}

/// HostFileWatcher monitors directory changes via macOS FSEvents on UserInteractive GCD queue
/// and streams binary protocol frames to Linux guest over vsock port 9922 in < 1ms.
public final class HostFileWatcher: @unchecked Sendable {
    public var watchPaths: [String]
    public var transport: EventStreamTransport?

    private var eventStream: FSEventStreamRef?
    private let eventQueue = DispatchQueue(label: "com.orbstack_free.fsevents", qos: .userInteractive)
    private let ackQueue = DispatchQueue(label: "com.orbstack_free.ack_reader", qos: .userInitiated)
    private let stateLock = NSLock()

    private var isRunning: Bool = false
    private var sequenceNumber: UInt64 = 0
    private var totalEventsSent: UInt64 = 0
    private var totalBytesSent: UInt64 = 0
    private var lastLatencyUs: Double = 0.0
    private var latencySumUs: Double = 0.0
    private var latencySampleCount: UInt64 = 0

    public var onEventDispatched: ((FileEventFrame) -> Void)?

    public init(watchPaths: [String], transport: EventStreamTransport? = nil) {
        self.watchPaths = watchPaths.map { ($0 as NSString).standardizingPath }
        self.transport = transport
    }

    public static func getHostTimeNs() -> UInt64 {
        var ts = timespec()
        clock_gettime(CLOCK_REALTIME, &ts)
        return UInt64(ts.tv_sec) * 1_000_000_000 + UInt64(ts.tv_nsec)
    }

    public static func mapFSEventsToInotify(_ flags: FSEventStreamEventFlags) -> UInt32 {
        var mask: UInt32 = 0
        if (flags & FSEventStreamEventFlags(kFSEventStreamEventFlagItemCreated)) != 0 {
            mask |= ORB_IN_CREATE
        }
        if (flags & FSEventStreamEventFlags(kFSEventStreamEventFlagItemRemoved)) != 0 {
            mask |= ORB_IN_DELETE
        }
        if (flags & FSEventStreamEventFlags(kFSEventStreamEventFlagItemModified)) != 0 {
            mask |= (ORB_IN_MODIFY | ORB_IN_CLOSE_WRITE)
        }
        if (flags & FSEventStreamEventFlags(kFSEventStreamEventFlagItemInodeMetaMod)) != 0 {
            mask |= ORB_IN_ATTRIB
        }
        if (flags & FSEventStreamEventFlags(kFSEventStreamEventFlagItemRenamed)) != 0 {
            mask |= ORB_IN_MOVE_SELF
        }
        if (flags & FSEventStreamEventFlags(kFSEventStreamEventFlagItemIsDir)) != 0 {
            mask |= ORB_IN_ISDIR
        }
        if mask == 0 {
            mask = (ORB_IN_MODIFY | ORB_IN_ATTRIB)
        }
        return mask
    }

    public func start() -> Bool {
        stateLock.lock()
        defer { stateLock.unlock() }

        guard !isRunning, !watchPaths.isEmpty else { return false }

        let primaryPath = watchPaths[0]
        let prefix = primaryPath.hasSuffix("/") ? primaryPath : primaryPath + "/"
        let box = FSEventContextBox(watcher: self, watchPrefix: prefix)
        let retainedBoxPtr = Unmanaged.passRetained(box).toOpaque()

        var context = FSEventStreamContext(
            version: 0,
            info: retainedBoxPtr,
            retain: nil,
            release: { info in
                if let info = info {
                    Unmanaged<FSEventContextBox>.fromOpaque(info).release()
                }
            },
            copyDescription: nil
        )

        let callback: FSEventStreamCallback = { streamRef, clientCallBackInfo, numEvents, eventPaths, eventFlags, eventIds in
            guard let clientCallBackInfo = clientCallBackInfo else { return }
            let box = Unmanaged<FSEventContextBox>.fromOpaque(clientCallBackInfo).takeUnretainedValue()
            guard let watcher = box.watcher else { return }

            let paths = Unmanaged<CFArray>.fromOpaque(eventPaths).takeUnretainedValue() as! [String]
            let nowNs = HostFileWatcher.getHostTimeNs()

            for i in 0..<numEvents {
                let fullPath = paths[i]
                let flags = eventFlags[i]

                let relPath: String
                if fullPath.hasPrefix(box.watchPrefix) {
                    relPath = String(fullPath.dropFirst(box.watchPrefix.count))
                } else if fullPath == box.watchPrefix.dropLast() {
                    relPath = ""
                } else {
                    relPath = fullPath
                }

                let inotifyMask = HostFileWatcher.mapFSEventsToInotify(flags)
                watcher.dispatchEvent(relativePath: relPath, inotifyMask: inotifyMask, timestampNs: nowNs)
            }
        }

        let cfPaths = watchPaths as CFArray
        guard let stream = FSEventStreamCreate(
            kCFAllocatorDefault,
            callback,
            &context,
            cfPaths,
            FSEventStreamEventId(kFSEventStreamEventIdSinceNow),
            0.0001, // 100 microseconds latency
            FSEventStreamCreateFlags(
                kFSEventStreamCreateFlagFileEvents |
                kFSEventStreamCreateFlagNoDefer |
                kFSEventStreamCreateFlagWatchRoot |
                kFSEventStreamCreateFlagUseCFTypes
            )
        ) else {
            return false
        }

        self.eventStream = stream
        FSEventStreamSetDispatchQueue(stream, eventQueue)
        guard FSEventStreamStart(stream) else {
            FSEventStreamInvalidate(stream)
            FSEventStreamRelease(stream)
            self.eventStream = nil
            return false
        }

        self.isRunning = true
        startAckReader()
        return true
    }

    public func stop() {
        stateLock.lock()
        defer { stateLock.unlock() }

        guard isRunning, let stream = eventStream else { return }
        FSEventStreamStop(stream)
        FSEventStreamInvalidate(stream)
        FSEventStreamRelease(stream)
        self.eventStream = nil
        self.isRunning = false
    }

    public func dispatchEvent(relativePath: String, inotifyMask: UInt32, timestampNs: UInt64 = HostFileWatcher.getHostTimeNs()) {
        stateLock.lock()
        sequenceNumber += 1
        let seq = sequenceNumber
        stateLock.unlock()

        let frame = FileEventFrame(
            opcode: ORB_OP_EVENT,
            inotifyMask: inotifyMask,
            seq: seq,
            timestampNs: timestampNs,
            relativePath: relativePath
        )

        let encoded = frame.encode()

        if let transport = self.transport {
            let written = transport.write(data: encoded)
            if written > 0 {
                stateLock.lock()
                totalEventsSent += 1
                totalBytesSent += UInt64(written)
                stateLock.unlock()
            }
        }

        onEventDispatched?(frame)
    }

    public func recordAck(sequence: UInt64, originalTimestampNs: UInt64) {
        let nowNs = Self.getHostTimeNs()
        if nowNs >= originalTimestampNs {
            let rttNs = nowNs - originalTimestampNs
            let rttUs = Double(rttNs) / 1000.0

            stateLock.lock()
            lastLatencyUs = rttUs
            latencySumUs += rttUs
            latencySampleCount += 1
            stateLock.unlock()
        }
    }

    private func startAckReader() {
        ackQueue.async { [weak self] in
            let hdrSize = 32
            var buf = [UInt8](repeating: 0, count: hdrSize)

            while true {
                guard let self = self else { return }
                self.stateLock.lock()
                let running = self.isRunning
                guard let t = self.transport else {
                    self.stateLock.unlock()
                    return
                }
                self.stateLock.unlock()

                if !running { return }

                var total = 0
                while total < hdrSize {
                    let r = t.read(into: &buf, maxCount: hdrSize - total)
                    if r <= 0 { return }
                    total += r
                }

                let magic = buf[0..<4].withUnsafeBytes { $0.loadUnaligned(as: UInt32.self) }
                let opcode = buf[6..<8].withUnsafeBytes { $0.loadUnaligned(as: UInt16.self) }

                if magic == ORB_FS_MAGIC && opcode == ORB_OP_ACK {
                    let seq = buf[12..<20].withUnsafeBytes { $0.loadUnaligned(as: UInt64.self) }
                    let origTs = buf[20..<28].withUnsafeBytes { $0.loadUnaligned(as: UInt64.self) }
                    self.recordAck(sequence: seq, originalTimestampNs: origTs)
                }
            }
        }
    }

    public func getStats() -> FSEventsStats {
        stateLock.lock()
        defer { stateLock.unlock() }

        let avg = latencySampleCount > 0 ? (latencySumUs / Double(latencySampleCount)) : 0.0
        return FSEventsStats(
            events_sent: totalEventsSent,
            bytes_sent: totalBytesSent,
            last_latency_us: lastLatencyUs,
            average_latency_us: avg,
            watched_paths: watchPaths
        )
    }
}
