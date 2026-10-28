import Foundation

// MARK: - JSON-RPC 2.0 Types

public enum RPCID: Codable, Equatable, Hashable {
    case int(Int)
    case string(String)

    public init(from decoder: Decoder) throws {
        let container = try decoder.singleValueContainer()
        if let intVal = try? container.decode(Int.self) {
            self = .int(intVal)
        } else if let strVal = try? container.decode(String.self) {
            self = .string(strVal)
        } else {
            throw DecodingError.typeMismatch(RPCID.self, DecodingError.Context(codingPath: decoder.codingPath, debugDescription: "Expected Int or String"))
        }
    }

    public func encode(to encoder: Encoder) throws {
        var container = encoder.singleValueContainer()
        switch self {
        case .int(let v):
            try container.encode(v)
        case .string(let v):
            try container.encode(v)
        }
    }
}

public struct RPCRequest: Codable {
    public var jsonrpc: String = "2.0"
    public var id: RPCID?
    public var method: String
    public var params: [String: AnyCodableValue]?

    public init(method: String, params: [String: AnyCodableValue]? = nil, id: RPCID? = .int(1)) {
        self.jsonrpc = "2.0"
        self.method = method
        self.params = params
        self.id = id
    }
}

public struct RPCResponse: Codable {
    public var jsonrpc: String = "2.0"
    public var id: RPCID?
    public var result: AnyCodableValue?
    public var error: RPCError?

    public init(id: RPCID?, result: AnyCodableValue?) {
        self.jsonrpc = "2.0"
        self.id = id
        self.result = result
        self.error = nil
    }

    public init(id: RPCID?, error: RPCError) {
        self.jsonrpc = "2.0"
        self.id = id
        self.result = nil
        self.error = error
    }
}

public struct RPCError: Codable, Error {
    public var code: Int
    public var message: String
    public var data: AnyCodableValue?

    public init(code: Int, message: String, data: AnyCodableValue? = nil) {
        self.code = code
        self.message = message
        self.data = data
    }

    public static let parseError     = RPCError(code: -32700, message: "Parse error")
    public static let invalidRequest = RPCError(code: -32600, message: "Invalid Request")
    public static let methodNotFound = RPCError(code: -32601, message: "Method not found")
    public static let invalidParams  = RPCError(code: -32602, message: "Invalid params")
    public static let internalError  = RPCError(code: -32603, message: "Internal error")
}

// MARK: - Type-erased JSON Value

public enum AnyCodableValue: Codable, Equatable, Sendable {
    case string(String)
    case int(Int)
    case double(Double)
    case bool(Bool)
    case dictionary([String: AnyCodableValue])
    case array([AnyCodableValue])
    case null

    public init(from decoder: Decoder) throws {
        let container = try decoder.singleValueContainer()
        if container.decodeNil() {
            self = .null
        } else if let b = try? container.decode(Bool.self) {
            self = .bool(b)
        } else if let i = try? container.decode(Int.self) {
            self = .int(i)
        } else if let d = try? container.decode(Double.self) {
            self = .double(d)
        } else if let s = try? container.decode(String.self) {
            self = .string(s)
        } else if let arr = try? container.decode([AnyCodableValue].self) {
            self = .array(arr)
        } else if let dict = try? container.decode([String: AnyCodableValue].self) {
            self = .dictionary(dict)
        } else {
            throw DecodingError.typeMismatch(AnyCodableValue.self, DecodingError.Context(codingPath: decoder.codingPath, debugDescription: "Unknown value type"))
        }
    }

    public func encode(to encoder: Encoder) throws {
        var container = encoder.singleValueContainer()
        switch self {
        case .string(let s): try container.encode(s)
        case .int(let i): try container.encode(i)
        case .double(let d): try container.encode(d)
        case .bool(let b): try container.encode(b)
        case .dictionary(let d): try container.encode(d)
        case .array(let a): try container.encode(a)
        case .null: try container.encodeNil()
        }
    }
}

// MARK: - Domain DTOs

public struct VMSummary: Identifiable, Codable, Hashable {
    public var id: String { name }
    public var name: String
    public var distro: String
    public var version: String
    public var arch: String
    public var state: String
    public var cpus: Int
    public var memory_mb: Int
    public var ip_address: String
    public var size: String
    public var uptime_sec: Int64
    public var created_at: String?

    public init(
        name: String,
        distro: String,
        version: String,
        arch: String,
        state: String,
        cpus: Int,
        memory_mb: Int,
        ip_address: String,
        size: String,
        uptime_sec: Int64,
        created_at: String? = nil
    ) {
        self.name = name
        self.distro = distro
        self.version = version
        self.arch = arch
        self.state = state
        self.cpus = cpus
        self.memory_mb = memory_mb
        self.ip_address = ip_address
        self.size = size
        self.uptime_sec = uptime_sec
        self.created_at = created_at
    }

    public var isRunning: Bool {
        state == "RUNNING"
    }
}

public struct DoctorCheckItem: Codable, Equatable {
    public var name: String
    public var ok: Bool
    public var detail: String

    public init(name: String, ok: Bool, detail: String) {
        self.name = name
        self.ok = ok
        self.detail = detail
    }
}

public struct DoctorReport: Codable, Equatable {
    public var overall_ok: Bool
    public var checks: [DoctorCheckItem]

    public init(overall_ok: Bool, checks: [DoctorCheckItem]) {
        self.overall_ok = overall_ok
        self.checks = checks
    }
}

public struct ContainerSummary: Identifiable, Codable, Hashable {
    public var id: String
    public var names: [String]
    public var image: String
    public var command: String
    public var state: String
    public var status: String
    public var ports: [String]
    public var mounts: [String: String]

    public init(
        id: String,
        names: [String],
        image: String,
        command: String,
        state: String,
        status: String,
        ports: [String],
        mounts: [String: String]
    ) {
        self.id = id
        self.names = names
        self.image = image
        self.command = command
        self.state = state
        self.status = status
        self.ports = ports
        self.mounts = mounts
    }

    public var displayName: String {
        names.first?.trimmingCharacters(in: CharacterSet(charactersIn: "/")) ?? id.prefix(12).description
    }
}

public struct LiveMetrics: Codable, Equatable {
    public var timestamp: Int64
    public var host_cpu_percent: Double
    public var guest_cpu_percent: Double
    public var memory_used_mb: Int64
    public var memory_total_mb: Int64
    public var balloon_freed_mb: Int64
    public var virtiofs_ops_per_sec: Int64
    public var virtiofs_cache_hit_rate: Double
    public var network_rx_kbps: Double
    public var network_tx_kbps: Double

    public init(
        timestamp: Int64 = Int64(Date().timeIntervalSince1970),
        host_cpu_percent: Double = 0.0,
        guest_cpu_percent: Double = 0.0,
        memory_used_mb: Int64 = 0,
        memory_total_mb: Int64 = 0,
        balloon_freed_mb: Int64 = 0,
        virtiofs_ops_per_sec: Int64 = 0,
        virtiofs_cache_hit_rate: Double = 0.0,
        network_rx_kbps: Double = 0.0,
        network_tx_kbps: Double = 0.0
    ) {
        self.timestamp = timestamp
        self.host_cpu_percent = host_cpu_percent
        self.guest_cpu_percent = guest_cpu_percent
        self.memory_used_mb = memory_used_mb
        self.memory_total_mb = memory_total_mb
        self.balloon_freed_mb = balloon_freed_mb
        self.virtiofs_ops_per_sec = virtiofs_ops_per_sec
        self.virtiofs_cache_hit_rate = virtiofs_cache_hit_rate
        self.network_rx_kbps = network_rx_kbps
        self.network_tx_kbps = network_tx_kbps
    }
}

public struct OrbSettings: Codable, Equatable {
    public var max_cpus: Int
    public var max_memory_mb: Int
    public var max_disk_gb: Int
    public var rosetta_x86: Bool
    public var fast_virtiofs: Bool
    public var battery_saver: Bool
    public var auto_start: Bool
    public var dns_domain: String
    public var storage_location: String

    public init(
        max_cpus: Int = 10,
        max_memory_mb: Int = 8192,
        max_disk_gb: Int = 64,
        rosetta_x86: Bool = true,
        fast_virtiofs: Bool = true,
        battery_saver: Bool = false,
        auto_start: Bool = true,
        dns_domain: String = "orb.local",
        storage_location: String = "/Volumes/AdamSSD/antigravity_workspace/orbstack_free/data"
    ) {
        self.max_cpus = max_cpus
        self.max_memory_mb = max_memory_mb
        self.max_disk_gb = max_disk_gb
        self.rosetta_x86 = rosetta_x86
        self.fast_virtiofs = fast_virtiofs
        self.battery_saver = battery_saver
        self.auto_start = auto_start
        self.dns_domain = dns_domain
        self.storage_location = storage_location
    }
}

public struct BalloonStats: Codable, Equatable {
    public var resident_mb: Double
    public var footprint_mb: Double
    public var target_ram_mb: UInt64
    public var avail_ram_mb: UInt64
    public var psi_some_percent: Double
    public var psi_full_percent: Double
    public var compaction_requested: Bool
    public var idle_ticks: Int

    public init(
        resident_mb: Double = 0.0,
        footprint_mb: Double = 0.0,
        target_ram_mb: UInt64 = 0,
        avail_ram_mb: UInt64 = 0,
        psi_some_percent: Double = 0.0,
        psi_full_percent: Double = 0.0,
        compaction_requested: Bool = false,
        idle_ticks: Int = 0
    ) {
        self.resident_mb = resident_mb
        self.footprint_mb = footprint_mb
        self.target_ram_mb = target_ram_mb
        self.avail_ram_mb = avail_ram_mb
        self.psi_some_percent = psi_some_percent
        self.psi_full_percent = psi_full_percent
        self.compaction_requested = compaction_requested
        self.idle_ticks = idle_ticks
    }
}

public struct FSEventsStats: Codable, Equatable {
    public var events_sent: UInt64
    public var bytes_sent: UInt64
    public var last_latency_us: Double
    public var average_latency_us: Double
    public var watched_paths: [String]

    public init(
        events_sent: UInt64 = 0,
        bytes_sent: UInt64 = 0,
        last_latency_us: Double = 0.0,
        average_latency_us: Double = 0.0,
        watched_paths: [String] = []
    ) {
        self.events_sent = events_sent
        self.bytes_sent = bytes_sent
        self.last_latency_us = last_latency_us
        self.average_latency_us = average_latency_us
        self.watched_paths = watched_paths
    }
}
