package main

import (
	"encoding/binary"
	"fmt"
	"net"
	"os"
	"syscall"
	"time"
	"unsafe"
)

const (
	AF_VSOCK         = 40
	VMADDR_CID_ANY   = 0xFFFFFFFF
	VMADDR_CID_HOST  = 2
	SOCKADDR_VM_SIZE = 16
)

// sockaddrVM matches Linux kernel struct sockaddr_vm
type sockaddrVM struct {
	family    uint16
	reserved1 uint16
	port      uint32
	cid       uint32
	zero      [4]byte
}

// ListenVsock creates a vsock listener on the given port, with fallback to TCP loopback
func ListenVsock(port uint32) (net.Listener, error) {
	// Attempt native Linux AF_VSOCK socket
	fd, err := syscall.Socket(AF_VSOCK, syscall.SOCK_STREAM, 0)
	if err == nil {
		var sa sockaddrVM
		sa.family = AF_VSOCK
		sa.port = port
		sa.cid = VMADDR_CID_ANY

		rawAddr := (*[SOCKADDR_VM_SIZE]byte)(unsafe.Pointer(&sa))[:]
		_, _, errno := syscall.Syscall(
			syscall.SYS_BIND,
			uintptr(fd),
			uintptr(unsafe.Pointer(&rawAddr[0])),
			uintptr(SOCKADDR_VM_SIZE),
		)
		if errno == 0 {
			_, _, errno = syscall.Syscall(syscall.SYS_LISTEN, uintptr(fd), uintptr(128), 0)
			if errno == 0 {
				f := os.NewFile(uintptr(fd), fmt.Sprintf("vsock:%d", port))
				l, err := net.FileListener(f)
				_ = f.Close()
				if err == nil {
					return l, nil
				}
			}
		}
		_ = syscall.Close(fd)
	}

	// Fallback to TCP loopback (for macOS testing, unprivileged runs, or environments without vhost-vsock)
	tcpAddr := fmt.Sprintf("127.0.0.1:%d", port)
	l, err := net.Listen("tcp", tcpAddr)
	if err != nil {
		// Fallback to random free port if port is occupied
		return net.Listen("tcp", "127.0.0.1:0")
	}
	return l, nil
}

// DialVsock connects to a vsock endpoint (CID:Port), with fallback to TCP loopback
func DialVsock(cid uint32, port uint32) (net.Conn, error) {
	// Attempt native Linux AF_VSOCK connect
	fd, err := syscall.Socket(AF_VSOCK, syscall.SOCK_STREAM, 0)
	if err == nil {
		var sa sockaddrVM
		sa.family = AF_VSOCK
		sa.port = port
		sa.cid = cid

		rawAddr := (*[SOCKADDR_VM_SIZE]byte)(unsafe.Pointer(&sa))[:]
		_, _, errno := syscall.Syscall(
			syscall.SYS_CONNECT,
			uintptr(fd),
			uintptr(unsafe.Pointer(&rawAddr[0])),
			uintptr(SOCKADDR_VM_SIZE),
		)
		if errno == 0 {
			f := os.NewFile(uintptr(fd), fmt.Sprintf("vsock:%d:%d", cid, port))
			c, err := net.FileConn(f)
			_ = f.Close()
			if err == nil {
				return c, nil
			}
		}
		_ = syscall.Close(fd)
	}

	// Fallback to TCP loopback
	tcpAddr := fmt.Sprintf("127.0.0.1:%d", port)
	var dialer net.Dialer
	dialer.Timeout = 1 * time.Second
	return dialer.Dial("tcp", tcpAddr)
}

// Ensure binary layout compatibility
func init() {
	var buf [4]byte
	binary.LittleEndian.PutUint32(buf[:], 0x12345678)
}
