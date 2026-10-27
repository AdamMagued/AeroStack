package main

import (
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// DockerForwarderConfig holds configuration for the guest Docker vsock forwarder
type DockerForwarderConfig struct {
	VsockPort      uint32
	DockerSockPath string
}

// DockerForwarder bridges host vsock connections directly into guest dockerd
type DockerForwarder struct {
	config      DockerForwarderConfig
	listener    net.Listener
	stopCh      chan struct{}
	wg          sync.WaitGroup
	running     int32
	activeMu    sync.Mutex
	activeConns map[net.Conn]struct{}
	totalProxied uint64
}

// NewDockerForwarder creates a new Docker vsock forwarder
func NewDockerForwarder(cfg DockerForwarderConfig) *DockerForwarder {
	if cfg.VsockPort == 0 {
		cfg.VsockPort = 2375
	}
	if cfg.DockerSockPath == "" {
		cfg.DockerSockPath = "/var/run/docker.sock"
	}

	return &DockerForwarder{
		config:      cfg,
		stopCh:      make(chan struct{}),
		activeConns: make(map[net.Conn]struct{}),
	}
}

// Start begins listening on vsock port 2375
func (f *DockerForwarder) Start() error {
	if !atomic.CompareAndSwapInt32(&f.running, 0, 1) {
		return fmt.Errorf("docker forwarder already running")
	}

	l, err := ListenVsock(f.config.VsockPort)
	if err != nil {
		atomic.StoreInt32(&f.running, 0)
		return fmt.Errorf("failed to listen on vsock port %d: %w", f.config.VsockPort, err)
	}
	f.listener = l

	f.wg.Add(1)
	go f.acceptLoop()

	log.Printf("[orb-agent:docker] Docker vsock forwarder listening on port %d -> %s",
		f.config.VsockPort, f.config.DockerSockPath)
	return nil
}

// Stop terminates the forwarder and closes active connections
func (f *DockerForwarder) Stop() {
	if atomic.CompareAndSwapInt32(&f.running, 1, 0) {
		close(f.stopCh)
		if f.listener != nil {
			_ = f.listener.Close()
		}

		f.activeMu.Lock()
		for c := range f.activeConns {
			_ = c.Close()
		}
		f.activeMu.Unlock()

		f.wg.Wait()
		log.Printf("[orb-agent:docker] Docker forwarder stopped (Total proxied: %d)",
			atomic.LoadUint64(&f.totalProxied))
	}
}

func (f *DockerForwarder) acceptLoop() {
	defer f.wg.Done()
	for {
		clientConn, err := f.listener.Accept()
		if err != nil {
			if atomic.LoadInt32(&f.running) == 0 {
				return
			}
			continue
		}

		f.activeMu.Lock()
		f.activeConns[clientConn] = struct{}{}
		f.activeMu.Unlock()

		atomic.AddUint64(&f.totalProxied, 1)
		go f.proxyConn(clientConn)
	}
}

func (f *DockerForwarder) proxyConn(clientConn net.Conn) {
	defer func() {
		_ = clientConn.Close()
		f.activeMu.Lock()
		delete(f.activeConns, clientConn)
		f.activeMu.Unlock()
	}()

	var dialer net.Dialer
	dialer.Timeout = 3 * time.Second
	backendConn, err := dialer.Dial("unix", f.config.DockerSockPath)
	if err != nil {
		return
	}
	defer backendConn.Close()

	// Bidirectional zero-copy splicing with half-close handling
	var wg sync.WaitGroup
	wg.Add(2)

	// client -> backend
	go func() {
		defer wg.Done()
		_, _ = io.Copy(backendConn, clientConn)
		if cw, ok := backendConn.(interface{ CloseWrite() error }); ok {
			_ = cw.CloseWrite()
		}
	}()

	// backend -> client
	go func() {
		defer wg.Done()
		_, _ = io.Copy(clientConn, backendConn)
		if cw, ok := clientConn.(interface{ CloseWrite() error }); ok {
			_ = cw.CloseWrite()
		}
	}()

	wg.Wait()
}
