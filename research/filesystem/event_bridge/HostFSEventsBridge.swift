import Foundation
import CoreServices
import Darwin

let ORB_FS_MAGIC: UInt32 = 0x4F524246  // "ORBF"
let ORB_FS_VERSION: UInt16 = 1

let ORB_OP_EVENT: UInt16 = 1
let ORB_OP_PING: UInt16 = 2
let ORB_OP_PONG: UInt16 = 3

let ORB_IN_ACCESS: UInt32        = 0x00000001
let ORB_IN_MODIFY: UInt32        = 0x00000002
let ORB_IN_ATTRIB: UInt32        = 0x00000004
let ORB_IN_CLOSE_WRITE: UInt32   = 0x00000008
let ORB_IN_CLOSE_NOWRITE: UInt32 = 0x00000010
let ORB_IN_OPEN: UInt32          = 0x00000020
let ORB_IN_MOVED_FROM: UInt32    = 0x00000040
let ORB_IN_MOVED_TO: UInt32      = 0x00000080
let ORB_IN_CREATE: UInt32        = 0x00000100
let ORB_IN_DELETE: UInt32        = 0x00000200
let ORB_IN_DELETE_SELF: UInt32   = 0x00000400
let ORB_IN_MOVE_SELF: UInt32     = 0x00000800
let ORB_IN_ISDIR: UInt32         = 0x40000000

func getHostTimeNs() -> UInt64 {
    var ts = timespec()
    clock_gettime(CLOCK_REALTIME, &ts)
    return UInt64(ts.tv_sec) * 1_000_000_000 + UInt64(ts.tv_nsec)
}

func mapFSEventsToInotify(_ flags: FSEventStreamEventFlags) -> UInt32 {
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

guard CommandLine.arguments.count >= 3 else {
    print("Usage: \(CommandLine.arguments[0]) <watch_dir> <guest_port> [guest_ip]")
    exit(1)
}

let watchDir = (CommandLine.arguments[1] as NSString).standardizingPath
let port = UInt16(CommandLine.arguments[2])!
let ip = (CommandLine.arguments.count >= 4) ? CommandLine.arguments[3] : "127.0.0.1"

print("[Host FSEvents Bridge (Swift)] Watching directory: \(watchDir)")
print("[Host FSEvents Bridge (Swift)] Target guest: \(ip):\(port)")

// Connect to guest daemon
var sock: Int32 = -1
var retries = 50
while retries > 0 {
    sock = socket(AF_INET, SOCK_STREAM, 0)
    var flag: Int32 = 1
    setsockopt(sock, IPPROTO_TCP, TCP_NODELAY, &flag, socklen_t(MemoryLayout<Int32>.size))
    
    var addr = sockaddr_in()
    addr.sin_len = UInt8(MemoryLayout<sockaddr_in>.size)
    addr.sin_family = sa_family_t(AF_INET)
    addr.sin_port = port.bigEndian
    inet_pton(AF_INET, ip, &addr.sin_addr)
    
    let res = withUnsafePointer(to: &addr) {
        $0.withMemoryRebound(to: sockaddr.self, capacity: 1) {
            connect(sock, $0, socklen_t(MemoryLayout<sockaddr_in>.size))
        }
    }
    if res == 0 {
        print("[Host FSEvents Bridge (Swift)] Connected to guest daemon successfully!")
        break
    }
    close(sock)
    sock = -1
    retries -= 1
    usleep(100_000) // 100ms
}

guard sock >= 0 else {
    perror("connect to guest failed")
    exit(1)
}

var eventSeq: UInt64 = 0
let watchDirPrefix = watchDir.hasSuffix("/") ? watchDir : watchDir + "/"

class StreamContext {
    let sockFd: Int32
    let watchPrefix: String
    var seq: UInt64 = 0

    init(sockFd: Int32, watchPrefix: String) {
        self.sockFd = sockFd
        self.watchPrefix = watchPrefix
    }
}

let ctx = StreamContext(sockFd: Int32(sock), watchPrefix: watchDirPrefix)
let ctxPtr = Unmanaged.passRetained(ctx).toOpaque()

var streamContext = FSEventStreamContext(
    version: 0,
    info: ctxPtr,
    retain: nil,
    release: nil,
    copyDescription: nil
)

let streamCallback: FSEventStreamCallback = { streamRef, clientCallBackInfo, numEvents, eventPaths, eventFlags, eventIds in
    guard let clientCallBackInfo = clientCallBackInfo else { return }
    let context = Unmanaged<StreamContext>.fromOpaque(clientCallBackInfo).takeUnretainedValue()
    let nowNs = getHostTimeNs()
    
    let paths = Unmanaged<CFArray>.fromOpaque(eventPaths).takeUnretainedValue() as! [String]
    
    for i in 0..<numEvents {
        let fullPath = paths[i]
        let flags = eventFlags[i]
        
        let relPath: String
        if fullPath.hasPrefix(context.watchPrefix) {
            relPath = String(fullPath.dropFirst(context.watchPrefix.count))
        } else if fullPath == context.watchPrefix.dropLast() {
            relPath = ""
        } else {
            continue
        }
        
        let inotifyMask = mapFSEventsToInotify(flags)
        let pathBytes = Array(relPath.utf8)
        let pathLen = UInt32(pathBytes.count)
        
        context.seq += 1
        
        // Pack binary header
        var headerData = Data()
        var magic = ORB_FS_MAGIC
        var version = ORB_FS_VERSION
        var opcode = ORB_OP_EVENT
        var mask = inotifyMask
        var seq = context.seq
        var ts = nowNs
        var plen = pathLen
        
        headerData.append(Data(bytes: &magic, count: 4))
        headerData.append(Data(bytes: &version, count: 2))
        headerData.append(Data(bytes: &opcode, count: 2))
        headerData.append(Data(bytes: &mask, count: 4))
        headerData.append(Data(bytes: &seq, count: 8))
        headerData.append(Data(bytes: &ts, count: 8))
        headerData.append(Data(bytes: &plen, count: 4))
        headerData.append(contentsOf: pathBytes)
        
        headerData.withUnsafeBytes { rawBuffer in
            _ = write(context.sockFd, rawBuffer.baseAddress, rawBuffer.count)
        }
    }
}

let pathsToWatch = [watchDir] as CFArray
let stream = FSEventStreamCreate(
    kCFAllocatorDefault,
    streamCallback,
    &streamContext,
    pathsToWatch,
    FSEventStreamEventId(kFSEventStreamEventIdSinceNow),
    0.0001, // 100 microseconds latency
    FSEventStreamCreateFlags(
        kFSEventStreamCreateFlagFileEvents |
        kFSEventStreamCreateFlagNoDefer |
        kFSEventStreamCreateFlagWatchRoot |
        kFSEventStreamCreateFlagUseCFTypes
    )
)!

let queue = DispatchQueue(label: "com.orbstack_free.fsevents", qos: .userInteractive)
FSEventStreamSetDispatchQueue(stream, queue)
let started = FSEventStreamStart(stream)
guard started else {
    print("Failed to start FSEventStream")
    exit(1)
}

print("[Host FSEvents Bridge (Swift)] FSEventStream successfully running on userInteractive queue.")
fflush(stdout)

// Background reader thread for ACK packets
let ackQueue = DispatchQueue(label: "com.orbstack_free.ack_reader")
ackQueue.async {
    var ackHdr = (
        magic: UInt32(0),
        version: UInt16(0),
        opcode: UInt16(0),
        mask: UInt32(0),
        seq: UInt64(0),
        ts: UInt64(0),
        plen: UInt32(0)
    )
    let hdrSize = 4 + 2 + 2 + 4 + 8 + 8 + 4 // 32 bytes
    var buf = [UInt8](repeating: 0, count: hdrSize)
    
    while true {
        var total = 0
        while total < hdrSize {
            let r = buf.withUnsafeMutableBytes { raw in
                read(sock, raw.baseAddress!.advanced(by: total), hdrSize - total)
            }
            if r <= 0 { return }
            total += r
        }
        
        let nowNs = getHostTimeNs()
        let seq = buf[12..<20].withUnsafeBytes { $0.load(as: UInt64.self) }
        let origTs = buf[20..<28].withUnsafeBytes { $0.load(as: UInt64.self) }
        
        if nowNs >= origTs {
            let rttNs = nowNs - origTs
            let rttUs = Double(rttNs) / 1000.0
            let oneWayMs = rttUs / 2000.0
            print(String(format: "[ACK_RTT #%llu] RTT=%.2f us (%.3f ms) | One-Way Delivery=%.3f ms", seq, rttUs, rttUs / 1000.0, oneWayMs))
            fflush(stdout)
        }
    }
}

dispatchMain()
