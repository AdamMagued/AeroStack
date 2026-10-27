package main

import (
	"context"
	"fmt"
	"net"
	"time"
)

// TransportConfig defines backend connection parameters
type TransportConfig struct {
	// Mode can be "unix", "vsock", or "tcp"
	Mode string
	// SocketPath is used when Mode == "unix"
	SocketPath string
	// TCPAddr is used when Mode == "tcp"
	TCPAddr string
	// VsockCID and VsockPort are used when Mode == "vsock"
	VsockCID  uint32
	VsockPort uint32
	// DialTimeout is the maximum duration for establishing connection
	DialTimeout time.Duration
}

// BackendDialer abstracts connection establishment to guest dockerd
type BackendDialer interface {
	Dial(ctx context.Context) (net.Conn, error)
	String() string
}

// UnixDialer connects to a Unix domain socket
type UnixDialer struct {
	SocketPath string
	Timeout    time.Duration
}

func (d *UnixDialer) Dial(ctx context.Context) (net.Conn, error) {
	var netDialer net.Dialer
	netDialer.Timeout = d.Timeout
	return netDialer.DialContext(ctx, "unix", d.SocketPath)
}

func (d *UnixDialer) String() string {
	return fmt.Sprintf("unix://%s", d.SocketPath)
}

// TCPDialer connects to a TCP socket
type TCPDialer struct {
	Address string
	Timeout time.Duration
}

func (d *TCPDialer) Dial(ctx context.Context) (net.Conn, error) {
	var netDialer net.Dialer
	netDialer.Timeout = d.Timeout
	return netDialer.DialContext(ctx, "tcp", d.Address)
}

func (d *TCPDialer) String() string {
	return fmt.Sprintf("tcp://%s", d.Address)
}

// VsockDialer connects to a Virtio Socket device
// On macOS host, VZVirtioSocketDevice uses custom bridging or native helper.
// On Linux/macOS socket bridge, it connects via AF_VSOCK or forwarded socket descriptor.
type VsockDialer struct {
	CID     uint32
	Port    uint32
	Timeout time.Duration
	// BridgeSocketPath is used when macOS daemon bridges vsock to a local Unix socket
	BridgeSocketPath string
}

func (d *VsockDialer) Dial(ctx context.Context) (net.Conn, error) {
	if d.BridgeSocketPath != "" {
		var netDialer net.Dialer
		netDialer.Timeout = d.Timeout
		return netDialer.DialContext(ctx, "unix", d.BridgeSocketPath)
	}
	// Direct vsock dial (using Linux AF_VSOCK sockaddr_vm or macOS vsock bridge)
	// For standard Go net package, vsock can be dialed using "vsock" network if supported
	var netDialer net.Dialer
	netDialer.Timeout = d.Timeout
	addr := fmt.Sprintf("%d:%d", d.CID, d.Port)
	conn, err := netDialer.DialContext(ctx, "vsock", addr)
	if err == nil {
		return conn, nil
	}
	// Fallback to loopback TCP proxy if vsock net is not directly exposed to userspace
	fallbackAddr := fmt.Sprintf("127.0.0.1:%d", d.Port)
	return netDialer.DialContext(ctx, "tcp", fallbackAddr)
}

func (d *VsockDialer) String() string {
	if d.BridgeSocketPath != "" {
		return fmt.Sprintf("vsock-bridge://%s (CID %d:%d)", d.BridgeSocketPath, d.CID, d.Port)
	}
	return fmt.Sprintf("vsock://%d:%d", d.CID, d.Port)
}

// CreateDialer constructs the appropriate dialer from config
func CreateDialer(cfg TransportConfig) (BackendDialer, error) {
	timeout := cfg.DialTimeout
	if timeout == 0 {
		timeout = 5 * time.Second
	}

	switch cfg.Mode {
	case "unix":
		if cfg.SocketPath == "" {
			return nil, fmt.Errorf("socket path required for unix mode")
		}
		return &UnixDialer{SocketPath: cfg.SocketPath, Timeout: timeout}, nil
	case "tcp":
		if cfg.TCPAddr == "" {
			return nil, fmt.Errorf("tcp address required for tcp mode")
		}
		return &TCPDialer{Address: cfg.TCPAddr, Timeout: timeout}, nil
	case "vsock":
		return &VsockDialer{
			CID:              cfg.VsockCID,
			Port:             cfg.VsockPort,
			Timeout:          timeout,
			BridgeSocketPath: cfg.SocketPath,
		}, nil
	default:
		return nil, fmt.Errorf("unsupported transport mode: %s", cfg.Mode)
	}
}
