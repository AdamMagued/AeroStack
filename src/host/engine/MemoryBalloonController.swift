import Foundation
import Virtualization
import Darwin

public let VSOCK_MEM_PORT: UInt32 = 1024
public let VSOCK_MEM_MAGIC: UInt32 = 0x4D454D31 // "MEM1"

public enum MemMessageType: UInt8 {
    case telemetryReport = 1
    case pressureAlert   = 2
    case compactCommand  = 3
    case setTargetAck    = 4
    case heartbeat       = 5
}

// 32-byte binary telemetry frame (Guest -> Host)
public struct GuestTelemetryFrame: Sendable {
    public var magic: UInt32
    public var msgType: UInt8
    public var reserved: (UInt8, UInt8, UInt8)
    public var totalRamBytes: UInt64
    public var freeRamBytes: UInt64
    public var availRamBytes: UInt64
    public var psiSomeAvg10: UInt32 // e.g. 125 = 1.25%
    public var psiFullAvg10: UInt32

    public init(
        magic: UInt32 = VSOCK_MEM_MAGIC,
        msgType: UInt8 = MemMessageType.telemetryReport.rawValue,
        totalRamBytes: UInt64 = 4096 * 1024 * 1024,
        freeRamBytes: UInt64 = 2048 * 1024 * 1024,
        availRamBytes: UInt64 = 3000 * 1024 * 1024,
        psiSomeAvg10: UInt32 = 0,
        psiFullAvg10: UInt32 = 0
    ) {
        self.magic = magic
        self.msgType = msgType
        self.reserved = (0, 0, 0)
        self.totalRamBytes = totalRamBytes
        self.freeRamBytes = freeRamBytes
        self.availRamBytes = availRamBytes
        self.psiSomeAvg10 = psiSomeAvg10
        self.psiFullAvg10 = psiFullAvg10
    }

    public static func decode(from data: Data) -> GuestTelemetryFrame? {
        guard data.count >= 40 else { return nil }
        return data.withUnsafeBytes { raw in
            let magic = raw.loadUnaligned(fromByteOffset: 0, as: UInt32.self)
            guard magic == VSOCK_MEM_MAGIC else { return nil }
            let msgType = raw.loadUnaligned(fromByteOffset: 4, as: UInt8.self)
            let totalRam = raw.loadUnaligned(fromByteOffset: 8, as: UInt64.self)
            let freeRam = raw.loadUnaligned(fromByteOffset: 16, as: UInt64.self)
            let availRam = raw.loadUnaligned(fromByteOffset: 24, as: UInt64.self)
            let psiSome = raw.loadUnaligned(fromByteOffset: 32, as: UInt32.self)
            let psiFull = raw.loadUnaligned(fromByteOffset: 36, as: UInt32.self)
            return GuestTelemetryFrame(
                magic: magic,
                msgType: msgType,
                totalRamBytes: totalRam,
                freeRamBytes: freeRam,
                availRamBytes: availRam,
                psiSomeAvg10: psiSome,
                psiFullAvg10: psiFull
            )
        }
    }
}

// 16-byte binary control frame (Host -> Guest)
public struct HostControlFrame: Sendable {
    public var magic: UInt32
    public var msgType: UInt8
    public var flags: UInt8 // 0x1: compact, 0x2: drop_caches, 0x4: trim_slab
    public var reserved: UInt16
    public var targetRamBytes: UInt64

    public init(
        magic: UInt32 = VSOCK_MEM_MAGIC,
        msgType: UInt8 = MemMessageType.compactCommand.rawValue,
        flags: UInt8 = 0x3,
        reserved: UInt16 = 0,
        targetRamBytes: UInt64 = 48 * 1024 * 1024
    ) {
        self.magic = magic
        self.msgType = msgType
        self.flags = flags
        self.reserved = reserved
        self.targetRamBytes = targetRamBytes
    }

    public func encode() -> Data {
        var data = Data(capacity: 16)
        var m = magic
        var t = msgType
        var f = flags
        var r = reserved
        var tr = targetRamBytes
        data.append(Data(bytes: &m, count: 4))
        data.append(Data(bytes: &t, count: 1))
        data.append(Data(bytes: &f, count: 1))
        data.append(Data(bytes: &r, count: 2))
        data.append(Data(bytes: &tr, count: 8))
        return data
    }
}

public protocol MemoryBalloonDeviceProtocol: AnyObject {
    var targetVirtualMachineMemorySize: UInt64 { get set }
}

extension VZVirtioTraditionalMemoryBalloonDevice: MemoryBalloonDeviceProtocol {}

/// Dynamic Balloon Controller implementing asymmetric hysteresis and PSI stall sensing
public final class MemoryBalloonController: @unchecked Sendable {
    public weak var balloonDevice: MemoryBalloonDeviceProtocol?

    // Tuning Parameters
    public var minRamMB: UInt64 = 48
    public var maxRamMB: UInt64 = 8192
    public var lowWatermarkAvailMB: UInt64 = 50
    public var targetIdleReserveMB: UInt64 = 40
    public var burstStepMB: UInt64 = 128
    public var reclaimStepMB: UInt64 = 64
    public var idleHoldThresholdTicks: Int = 4 // 1.0s at 250ms cadence

    // Controller State
    public private(set) var idleTicks: Int = 0
    public private(set) var compactionRequested: Bool = false
    public private(set) var lastTelemetry: GuestTelemetryFrame?
    public private(set) var currentTargetMB: UInt64 = 512

    public var sendControlFrameHandler: ((HostControlFrame) -> Void)?

    private let queue = DispatchQueue(label: "dev.orbstack_free.memory_controller", qos: .userInteractive)
    private let stateLock = NSLock()

    public init(balloonDevice: MemoryBalloonDeviceProtocol? = nil, initialTargetMB: UInt64 = 512) {
        self.balloonDevice = balloonDevice
        self.currentTargetMB = initialTargetMB
        if let dev = balloonDevice {
            dev.targetVirtualMachineMemorySize = initialTargetMB * 1024 * 1024
        }
    }

    /// Primary reactive controller hook: processes incoming telemetry and adjusts memory targets
    public func handleTelemetry(telemetry: GuestTelemetryFrame) {
        queue.async { [weak self] in
            guard let self = self else { return }
            self.stateLock.lock()
            self.lastTelemetry = telemetry
            let availMB = telemetry.availRamBytes / (1024 * 1024)
            let activeTargetMB = self.balloonDevice?.targetVirtualMachineMemorySize != nil 
                ? (self.balloonDevice!.targetVirtualMachineMemorySize / (1024 * 1024))
                : self.currentTargetMB
            let psiSome = Double(telemetry.psiSomeAvg10) / 100.0
            self.stateLock.unlock()

            // 1. FAST PATH: PSI Stall Hazard or Low Available RAM -> IMMEDIATE BURST
            if psiSome > 0.05 || availMB < self.lowWatermarkAvailMB {
                self.stateLock.lock()
                self.idleTicks = 0
                self.compactionRequested = false

                let neededMB = (self.lowWatermarkAvailMB > availMB ? (self.lowWatermarkAvailMB - availMB) : 0) + self.burstStepMB
                let newTargetMB = min(self.maxRamMB, activeTargetMB + neededMB)
                
                if newTargetMB > activeTargetMB {
                    self.currentTargetMB = newTargetMB
                    self.balloonDevice?.targetVirtualMachineMemorySize = newTargetMB * 1024 * 1024
                }
                self.stateLock.unlock()

                // Notify guest of new target memory ceiling
                let ackFrame = HostControlFrame(
                    msgType: MemMessageType.setTargetAck.rawValue,
                    flags: 0,
                    targetRamBytes: newTargetMB * 1024 * 1024
                )
                self.sendControlFrameHandler?(ackFrame)
                return
            }

            // 2. SLOW PATH: Sustained Idle -> Proactive Compaction & Gradual Step-Down
            if availMB > (self.targetIdleReserveMB + self.reclaimStepMB) {
                self.stateLock.lock()
                self.idleTicks += 1
                let ticks = self.idleTicks
                let alreadyCompacted = self.compactionRequested
                self.stateLock.unlock()

                if ticks >= self.idleHoldThresholdTicks {
                    if !alreadyCompacted {
                        self.stateLock.lock()
                        self.compactionRequested = true
                        self.stateLock.unlock()

                        // Send compaction command (compact + drop_caches)
                        let compactFrame = HostControlFrame(
                            msgType: MemMessageType.compactCommand.rawValue,
                            flags: 0x3,
                            targetRamBytes: activeTargetMB * 1024 * 1024
                        )
                        self.sendControlFrameHandler?(compactFrame)
                    } else {
                        // Gradual step-down reclamation
                        self.stateLock.lock()
                        let reclaimableMB = availMB - self.targetIdleReserveMB
                        let stepMB = min(self.reclaimStepMB, reclaimableMB)
                        let newTargetMB = max(self.minRamMB, activeTargetMB > stepMB ? (activeTargetMB - stepMB) : self.minRamMB)

                        if newTargetMB < activeTargetMB {
                            self.currentTargetMB = newTargetMB
                            self.balloonDevice?.targetVirtualMachineMemorySize = newTargetMB * 1024 * 1024
                        }
                        self.stateLock.unlock()

                        let stepAck = HostControlFrame(
                            msgType: MemMessageType.setTargetAck.rawValue,
                            flags: 0,
                            targetRamBytes: newTargetMB * 1024 * 1024
                        )
                        self.sendControlFrameHandler?(stepAck)
                    }
                }
            } else {
                self.stateLock.lock()
                self.idleTicks = 0
                self.stateLock.unlock()
            }
        }
    }

    /// Mach VM Physical Footprint Auditor
    public static func queryHostFootprint() -> (residentMB: Double, footprintMB: Double) {
        var vmInfo = task_vm_info_data_t()
        var count = mach_msg_type_number_t(MemoryLayout<task_vm_info_data_t>.size / 4)
        _ = withUnsafeMutablePointer(to: &vmInfo) {
            $0.withMemoryRebound(to: integer_t.self, capacity: Int(count)) {
                task_info(mach_task_self_, task_flavor_t(TASK_VM_INFO), $0, &count)
            }
        }

        var basicInfo = mach_task_basic_info_data_t()
        var basicCount = mach_msg_type_number_t(MemoryLayout<mach_task_basic_info_data_t>.size / 4)
        _ = withUnsafeMutablePointer(to: &basicInfo) {
            $0.withMemoryRebound(to: integer_t.self, capacity: Int(basicCount)) {
                task_info(mach_task_self_, task_flavor_t(MACH_TASK_BASIC_INFO), $0, &basicCount)
            }
        }

        return (
            Double(basicInfo.resident_size) / (1024.0 * 1024.0),
            Double(vmInfo.phys_footprint) / (1024.0 * 1024.0)
        )
    }

    /// Snapshot current stats for RPC and GUI consumption
    public func getStats() -> BalloonStats {
        stateLock.lock()
        defer { stateLock.unlock() }

        let footprint = Self.queryHostFootprint()
        let availMB = (lastTelemetry?.availRamBytes ?? 0) / (1024 * 1024)
        let psiSome = Double(lastTelemetry?.psiSomeAvg10 ?? 0) / 100.0
        let psiFull = Double(lastTelemetry?.psiFullAvg10 ?? 0) / 100.0

        return BalloonStats(
            resident_mb: footprint.residentMB,
            footprint_mb: footprint.footprintMB,
            target_ram_mb: currentTargetMB,
            avail_ram_mb: availMB,
            psi_some_percent: psiSome,
            psi_full_percent: psiFull,
            compaction_requested: compactionRequested,
            idle_ticks: idleTicks
        )
    }
}
