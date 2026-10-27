import Foundation
import Virtualization
import Darwin

// Mach VM Footprint Helper
struct MachMemoryStats {
    let residentMB: Double
    let physFootprintMB: Double

    static func current() -> MachMemoryStats {
        var count = mach_msg_type_number_t(MemoryLayout<mach_task_basic_info_data_t>.size / 4)
        var basicInfo = mach_task_basic_info_data_t()
        let kerr1 = withUnsafeMutablePointer(to: &basicInfo) {
            $0.withMemoryRebound(to: integer_t.self, capacity: Int(count)) {
                task_info(mach_task_self_, task_flavor_t(MACH_TASK_BASIC_INFO), $0, &count)
            }
        }

        var vmInfo = task_vm_info_data_t()
        var count2 = mach_msg_type_number_t(MemoryLayout<task_vm_info_data_t>.size / 4)
        let kerr2 = withUnsafeMutablePointer(to: &vmInfo) {
            $0.withMemoryRebound(to: integer_t.self, capacity: Int(count2)) {
                task_info(mach_task_self_, task_flavor_t(TASK_VM_INFO), $0, &count2)
            }
        }

        let res = kerr1 == KERN_SUCCESS ? Double(basicInfo.resident_size) / (1024.0 * 1024.0) : 0.0
        let fp = kerr2 == KERN_SUCCESS ? Double(vmInfo.phys_footprint) / (1024.0 * 1024.0) : 0.0
        return MachMemoryStats(residentMB: res, physFootprintMB: fp)
    }
}

print("================================================================================")
print("macOS Virtualization.framework Memory Ballooning & Footprint Harness")
print("================================================================================")

let baseStats = MachMemoryStats.current()
print("Baseline Host RSS: \(String(format: "%.2f", baseStats.residentMB)) MB | Footprint: \(String(format: "%.2f", baseStats.physFootprintMB)) MB")

let config = VZVirtualMachineConfiguration()
config.cpuCount = 2
// Allocate 4GB virtual address space for VM
config.memorySize = 4 * 1024 * 1024 * 1024

print("Configured VM Max Memory Size: \(config.memorySize / (1024 * 1024)) MB")
print("Minimum Allowed Memory Size:   \(VZVirtualMachineConfiguration.minimumAllowedMemorySize / (1024 * 1024)) MB")
print("Maximum Allowed Memory Size:   \(VZVirtualMachineConfiguration.maximumAllowedMemorySize / (1024 * 1024)) MB")

// Add Memory Balloon Device Configuration
let balloonConfig = VZVirtioTraditionalMemoryBalloonDeviceConfiguration()
config.memoryBalloonDevices = [balloonConfig]
print("[OK] Attached VZVirtioTraditionalMemoryBalloonDeviceConfiguration")

// Setup Bootloader if kernel exists
let kernelPath = "/Applications/OrbStack.app/Contents/Resources/assets/release/arm64/kernel"
if FileManager.default.fileExists(atPath: kernelPath) {
    let bootLoader = VZLinuxBootLoader(kernelURL: URL(fileURLWithPath: kernelPath))
    bootLoader.commandLine = "console=hvc0 nomodules rw"
    config.bootLoader = bootLoader
    print("[OK] Attached VZLinuxBootLoader with kernel: \(kernelPath)")
} else {
    print("[WARN] Kernel not found at \(kernelPath)")
}

// Validate Configuration
do {
    try config.validate()
    print("[OK] VZVirtualMachineConfiguration validated successfully!")
} catch {
    print("[ERROR] Validation failed: \(error)")
}

let statsAfterConfig = MachMemoryStats.current()
print("Host RSS After Config: \(String(format: "%.2f", statsAfterConfig.residentMB)) MB | Footprint: \(String(format: "%.2f", statsAfterConfig.physFootprintMB)) MB")

// Check entitlement and instantiate VM object
let vm = VZVirtualMachine(configuration: config)
print("[OK] Instantiated VZVirtualMachine object (CanStart: \(vm.canStart))")

if let balloonDevice = vm.memoryBalloonDevices.first as? VZVirtioTraditionalMemoryBalloonDevice {
    print("[OK] Memory Balloon Device is active!")
    print("     Current targetVirtualMachineMemorySize: \(balloonDevice.targetVirtualMachineMemorySize / (1024 * 1024)) MB")
    
    // Demonstrate balloon dial down to idle baseline (e.g. 64MB)
    let idleTarget: UInt64 = 64 * 1024 * 1024
    balloonDevice.targetVirtualMachineMemorySize = idleTarget
    print("     New targetVirtualMachineMemorySize:     \(balloonDevice.targetVirtualMachineMemorySize / (1024 * 1024)) MB")
    
    // Demonstrate instant burst to 2048 MB
    let burstTarget: UInt64 = 2048 * 1024 * 1024
    balloonDevice.targetVirtualMachineMemorySize = burstTarget
    print("     Burst targetVirtualMachineMemorySize:   \(balloonDevice.targetVirtualMachineMemorySize / (1024 * 1024)) MB")
} else {
    print("[WARN] No VZVirtioTraditionalMemoryBalloonDevice found in vm.memoryBalloonDevices")
}

let finalStats = MachMemoryStats.current()
print("\nFinal Host RSS: \(String(format: "%.2f", finalStats.residentMB)) MB | Footprint: \(String(format: "%.2f", finalStats.physFootprintMB)) MB")
print("================================================================================")
