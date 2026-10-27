import Foundation
import Virtualization
import Darwin

/// Binary protocol structures matching vsock_mem_protocol.h
public struct GuestTelemetryFrame {
    public var magic: UInt32
    public var msgType: UInt8
    public var reserved: (UInt8, UInt8, UInt8)
    public var totalRamBytes: UInt64
    public var freeRamBytes: UInt64
    public var availRamBytes: UInt64
    public var psiSomeAvg10: UInt32
    public var psiFullAvg10: UInt32
}

public struct HostControlFrame {
    public var magic: UInt32
    public var msgType: UInt8
    public var flags: UInt8
    public var reserved: UInt16
    public var targetRamBytes: UInt64
}

/// Host Balloon Controller with Asymmetric Hysteresis & PSI-driven Bursts
public final class HostBalloonController {
    public let vm: VZVirtualMachine
    public let balloonDevice: VZVirtioTraditionalMemoryBalloonDevice
    
    // Tuning parameters
    public var minRamMB: UInt64 = 48
    public var maxRamMB: UInt64 = 4096
    public var lowWatermarkAvailMB: UInt64 = 50
    public var targetIdleReserveMB: UInt64 = 40
    public var burstStepMB: UInt64 = 128
    public var reclaimStepMB: UInt64 = 64
    
    // State
    private var idleTicks: Int = 0
    private let idleHoldThresholdTicks: Int = 4 // 1.0s at 250ms cadence
    private var compactionRequested: Bool = false
    private let queue = DispatchQueue(label: "dev.orbstack_free.memory_controller", qos: .userInteractive)
    private var timer: DispatchSourceTimer?
    
    public init?(vm: VZVirtualMachine) {
        self.vm = vm
        guard let balloon = vm.memoryBalloonDevices.first as? VZVirtioTraditionalMemoryBalloonDevice else {
            return nil
        }
        self.balloonDevice = balloon
    }
    
    /// Process incoming telemetry from Guest Agent
    public func handleTelemetry(telemetry: GuestTelemetryFrame) {
        queue.async {
            _ = telemetry.totalRamBytes / (1024 * 1024)
            let availMB = telemetry.availRamBytes / (1024 * 1024)
            let currentTargetMB = self.balloonDevice.targetVirtualMachineMemorySize / (1024 * 1024)
            let psiSome = Double(telemetry.psiSomeAvg10) / 100.0
            
            // 1. FAST PATH: PSI Stall or Low Memory Hazard -> IMMEDIATE BURST
            if psiSome > 0.05 || availMB < self.lowWatermarkAvailMB {
                self.idleTicks = 0
                self.compactionRequested = false
                
                let neededMB = (self.lowWatermarkAvailMB > availMB ? (self.lowWatermarkAvailMB - availMB) : 0) + self.burstStepMB
                let newTargetMB = min(self.maxRamMB, currentTargetMB + neededMB)
                
                if newTargetMB > currentTargetMB {
                    self.balloonDevice.targetVirtualMachineMemorySize = newTargetMB * 1024 * 1024
                    print("[CONTROLLER] ⚡ BURST: Stalled (\(psiSome)%). Target -> \(newTargetMB) MB")
                }
                return
            }
            
            // 2. SLOW PATH: Sustained Idle -> Proactive Compaction & Step-Down
            if availMB > (self.targetIdleReserveMB + self.reclaimStepMB) {
                self.idleTicks += 1
                if self.idleTicks >= self.idleHoldThresholdTicks {
                    if !self.compactionRequested {
                        self.compactionRequested = true
                        print("[CONTROLLER] 🧹 GUEST IDLE: Requesting compaction & cache flush")
                        // Send HostControlFrame with flags = 0x3 (compact + drop caches)
                    } else {
                        let reclaimableMB = availMB - self.targetIdleReserveMB
                        let stepMB = min(self.reclaimStepMB, reclaimableMB)
                        let newTargetMB = max(self.minRamMB, currentTargetMB - stepMB)
                        
                        if newTargetMB < currentTargetMB {
                            self.balloonDevice.targetVirtualMachineMemorySize = newTargetMB * 1024 * 1024
                            print("[CONTROLLER] 📉 RECLAIM: Reclaiming \(stepMB) MB -> New Target: \(newTargetMB) MB")
                        }
                    }
                }
            } else {
                self.idleTicks = 0
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
}
