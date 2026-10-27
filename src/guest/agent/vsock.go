package main

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
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

type VsockAddr struct {
	CID  uint32
	Port uint32
}

func (a *VsockAddr) Network() string { return "vsock" }
func (a *VsockAddr) String() string  { return fmt.Sprintf("vsock:%d:%d", a.CID, a.Port) }

type rawVsockConn struct {
	fd     int
	remote VsockAddr
	local  VsockAddr
	closed int32
}

func (c *rawVsockConn) Read(b []byte) (n int, err error) {
	if atomic.LoadInt32(&c.closed) == 1 {
		return 0, io.EOF
	}
	n, err = syscall.Read(c.fd, b)
	if n == 0 && err == nil {
		return 0, io.EOF
	}
	if err != nil {
		if atomic.LoadInt32(&c.closed) == 1 {
			return 0, io.EOF
		}
		return n, err
	}
	return n, nil
}

func (c *rawVsockConn) Write(b []byte) (n int, err error) {
	if atomic.LoadInt32(&c.closed) == 1 {
		return 0, io.ErrClosedPipe
	}
	return syscall.Write(c.fd, b)
}

func (c *rawVsockConn) Close() error {
	if atomic.CompareAndSwapInt32(&c.closed, 0, 1) {
		_ = syscall.Shutdown(c.fd, syscall.SHUT_RDWR)
		return syscall.Close(c.fd)
	}
	return nil
}

func (c *rawVsockConn) CloseWrite() error {
	return syscall.Shutdown(c.fd, syscall.SHUT_WR)
}

func (c *rawVsockConn) CloseRead() error {
	return syscall.Shutdown(c.fd, syscall.SHUT_RD)
}

func (c *rawVsockConn) LocalAddr() net.Addr                { return &c.local }
func (c *rawVsockConn) RemoteAddr() net.Addr               { return &c.remote }
func (c *rawVsockConn) SetDeadline(t time.Time) error      { return nil }
func (c *rawVsockConn) SetReadDeadline(t time.Time) error  { return nil }
func (c *rawVsockConn) SetWriteDeadline(t time.Time) error { return nil }

type rawVsockListener struct {
	fd     int
	port   uint32
	closed int32
	mu     sync.Mutex
}

func (l *rawVsockListener) Accept() (net.Conn, error) {
	for {
		if atomic.LoadInt32(&l.closed) == 1 {
			return nil, net.ErrClosed
		}
		var clientSA sockaddrVM
		saLen := uint32(SOCKADDR_VM_SIZE)
		clientFd, _, errno := syscall.Syscall(
			syscall.SYS_ACCEPT,
			uintptr(l.fd),
			uintptr(unsafe.Pointer(&clientSA)),
			uintptr(unsafe.Pointer(&saLen)),
		)
		if errno != 0 {
			if errno == syscall.EINTR {
				continue
			}
			if atomic.LoadInt32(&l.closed) == 1 {
				return nil, net.ErrClosed
			}
			return nil, errno
		}

		conn := &rawVsockConn{
			fd: int(clientFd),
			remote: VsockAddr{
				CID:  clientSA.cid,
				Port: clientSA.port,
			},
			local: VsockAddr{
				CID:  VMADDR_CID_ANY,
				Port: l.port,
			},
		}
		return conn, nil
	}
}

func (l *rawVsockListener) Close() error {
	if atomic.CompareAndSwapInt32(&l.closed, 0, 1) {
		_ = syscall.Shutdown(l.fd, syscall.SHUT_RDWR)
		return syscall.Close(l.fd)
	}
	return nil
}

func (l *rawVsockListener) Addr() net.Addr {
	return &VsockAddr{CID: VMADDR_CID_ANY, Port: l.port}
}

// ListenVsock creates a native Linux AF_VSOCK listener on the given port, with fallback to TCP loopback
func ListenVsock(port uint32) (net.Listener, error) {
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
				return &rawVsockListener{fd: fd, port: port}, nil
			}
		}
		_ = syscall.Close(fd)
	}

	// Fallback to TCP loopback (for macOS testing, unprivileged runs, or environments without vhost-vsock)
	tcpAddr := fmt.Sprintf("127.0.0.1:%d", port)
	l, err := net.Listen("tcp", tcpAddr)
	if err != nil {
		return net.Listen("tcp", "127.0.0.1:0")
	}
	return l, nil
}

// DialVsock connects to a vsock endpoint (CID:Port), with fallback to TCP loopback
func DialVsock(cid uint32, port uint32) (net.Conn, error) {
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
			return &rawVsockConn{
				fd:     fd,
				remote: VsockAddr{CID: cid, Port: port},
				local:  VsockAddr{CID: VMADDR_CID_ANY, Port: 0},
			}, nil
		}
		_ = syscall.Close(fd)
	}

	tcpAddr := fmt.Sprintf("127.0.0.1:%d", port)
	var dialer net.Dialer
	dialer.Timeout = 1 * time.Second
	return dialer.Dial("tcp", tcpAddr)
}

func init() {
	var buf [4]byte
	binary.LittleEndian.PutUint32(buf[:], 0x12345678)
}
