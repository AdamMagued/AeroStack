import Foundation
import Virtualization

print("Testing Virtualization framework network attachments...")

// 1. Test VZNATNetworkDeviceAttachment
let natAttachment = VZNATNetworkDeviceAttachment()
print("VZNATNetworkDeviceAttachment created successfully: \(natAttachment)")

// 2. Test VZFileHandleNetworkDeviceAttachment
var fds: [Int32] = [0, 0]
let ret = socketpair(AF_UNIX, SOCK_DGRAM, 0, &fds)
if ret == 0 {
    print("socketpair(AF_UNIX, SOCK_DGRAM, 0) created successfully: fds = [\(fds[0]), \(fds[1])]")
    let handle = FileHandle(fileDescriptor: fds[0], closeOnDealloc: true)
    let fileHandleAttachment = VZFileHandleNetworkDeviceAttachment(fileHandle: handle)
    print("VZFileHandleNetworkDeviceAttachment created successfully: \(fileHandleAttachment)")
    print("Default MTU: \(fileHandleAttachment.maximumTransmissionUnit)")
    fileHandleAttachment.maximumTransmissionUnit = 1500
    print("Set MTU: \(fileHandleAttachment.maximumTransmissionUnit)")
    close(fds[1])
} else {
    print("socketpair failed with errno: \(errno)")
}
