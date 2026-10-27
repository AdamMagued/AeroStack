import Foundation
import SwiftUI
import Combine

// MARK: - JSON-RPC Wire Protocol Models

struct RPCRequestEnvelope<T: Encodable>: Encodable {
    let jsonrpc: String = "2.0"
    let id: Int
    let method: String
    let params: T?
}

struct RPCResponseEnvelope<T: Decodable>: Decodable {
    let jsonrpc: String
    let id: Int?
    let result: T?
    let error: RPCErrorPayload?
}

struct RPCErrorPayload: Decodable {
    let code: Int
    let message: String
}

// MARK: - Domain UI Models

public struct LinuxMachineModel: Identifiable, Codable, Hashable {
    public var id: String { name }
    public let name: String
    public let distro: String
    public let version: String
    public let arch: String
    public var state: String
    public let cpus: Int
    public let memory_mb: Int
    public var ip_address: String
    public let size: String
    public var uptime_sec: Int64
    
    public var isRunning: Bool {
        state == "RUNNING"
    }
}

public struct ContainerModel: Identifiable, Codable, Hashable {
    public let id: String
    public let names: [String]
    public let image: String
    public let command: String
    public var state: String
    public var status: String
    public let ports: [String]
    public let mounts: [String: String]
    
    public var displayName: String {
        names.first?.trimmingCharacters(in: CharacterSet(charactersIn: "/")) ?? id.prefix(12).description
    }
}

public struct LiveMetricsModel: Codable {
    public let timestamp: Int64
    public let host_cpu_percent: Double
    public let guest_cpu_percent: Double
    public let memory_used_mb: Int64
    public let memory_total_mb: Int64
    public let balloon_freed_mb: Int64
    public let virtiofs_ops_per_sec: Int64
    public let virtiofs_cache_hit_rate: Double
    public let network_rx_kbps: Double
    public let network_tx_kbps: Double
}

public struct OrbSettingsModel: Codable {
    public var max_cpus: Int
    public var max_memory_mb: Int
    public var max_disk_gb: Int
    public var rosetta_x86: Bool
    public var fast_virtiofs: Bool
    public var battery_saver: Bool
    public var auto_start: Bool
    public var dns_domain: String
    public var storage_location: String
}

// MARK: - Unix Socket Client & Observable State Store

@MainActor
public class OrbDaemonStore: ObservableObject {
    @Published public var machines: [LinuxMachineModel] = []
    @Published public var containers: [ContainerModel] = []
    @Published public var metrics: LiveMetricsModel? = nil
    @Published public var settings: OrbSettingsModel? = nil
    @Published public var isConnected: Bool = false
    @Published public var errorMessage: String? = nil
    
    private let socketPath: String
    private var timer: Timer?
    
    public init(socketPath: String = ("~/.orbstack/run/vmcontrol.sock" as NSString).expandingTildeInPath) {
        self.socketPath = socketPath
        refreshAll()
        startMetricsPolling()
    }
    
    public func refreshAll() {
        fetchMachines()
        fetchContainers()
        fetchMetrics()
        fetchSettings()
    }
    
    private func startMetricsPolling() {
        timer = Timer.scheduledTimer(withTimeInterval: 1.0, repeats: true) { [weak self] _ in
            Task { @MainActor in
                self?.fetchMetrics()
            }
        }
    }
    
    public func fetchMachines() {
        // Sends {"jsonrpc":"2.0","id":1,"method":"vm.list"} over socket
        // Updates self.machines
    }
    
    public func fetchContainers() {
        // Sends {"jsonrpc":"2.0","id":2,"method":"container.list"}
    }
    
    public func fetchMetrics() {
        // Sends {"jsonrpc":"2.0","id":3,"method":"metrics.get"}
    }
    
    public func fetchSettings() {
        // Sends {"jsonrpc":"2.0","id":4,"method":"config.get"}
    }
    
    public func startMachine(name: String) {
        // vm.start
    }
    
    public func stopMachine(name: String) {
        // vm.stop
    }
    
    public func createMachine(distro: String, name: String, cpus: Int, memoryMB: Int) {
        // vm.create
    }
}
