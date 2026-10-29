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

// ForwardRule describes a port forwarding mapping
type ForwardRule struct {
	Protocol   string `json:"protocol"` // "tcp" or "udp"
	HostIP     string `json:"host_ip"`
	HostPort   int    `json:"host_port"`
	TargetIP   string `json:"target_ip"`
	TargetPort int    `json:"target_port"`
}

type activeForwarder struct {
	rule     ForwardRule
	listener net.Listener
	udpConn  *net.UDPConn
	stopCh   chan struct{}
	closed   int32
}

// PortForwarder provides userspace bi-directional port forwarding between macOS and guest containers
type PortForwarder struct {
	mu      sync.RWMutex
	rules   map[string]*activeForwarder
	stopCh  chan struct{}
	running int32
}

// NewPortForwarder initializes a userspace port forwarder
func NewPortForwarder() *PortForwarder {
	return &PortForwarder{
		rules:  make(map[string]*activeForwarder),
		stopCh: make(chan struct{}),
	}
}

// Start marks forwarder as active
func (pf *PortForwarder) Start() {
	atomic.StoreInt32(&pf.running, 1)
}

// Stop cleanly terminates all active forwarding listeners
func (pf *PortForwarder) Stop() {
	if atomic.CompareAndSwapInt32(&pf.running, 1, 0) {
		close(pf.stopCh)
		pf.mu.Lock()
		defer pf.mu.Unlock()
		for _, f := range pf.rules {
			atomic.StoreInt32(&f.closed, 1)
			close(f.stopCh)
			if f.listener != nil {
				_ = f.listener.Close()
			}
			if f.udpConn != nil {
				_ = f.udpConn.Close()
			}
		}
		pf.rules = make(map[string]*activeForwarder)
	}
}

// ForwardTCP binds hostPort on hostIP (default 127.0.0.1) and proxies connections to targetIP:targetPort
func (pf *PortForwarder) ForwardTCP(hostIP string, hostPort int, targetIP string, targetPort int) error {
	if hostIP == "" {
		hostIP = "127.0.0.1"
	}
	key := fmt.Sprintf("tcp:%s:%d", hostIP, hostPort)

	pf.mu.Lock()
	defer pf.mu.Unlock()

	if _, exists := pf.rules[key]; exists {
		return fmt.Errorf("forwarding rule already exists for %s", key)
	}

	addr := fmt.Sprintf("%s:%d", hostIP, hostPort)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("failed to bind host port %s: %w", addr, err)
	}

	rule := ForwardRule{
		Protocol:   "tcp",
		HostIP:     hostIP,
		HostPort:   hostPort,
		TargetIP:   targetIP,
		TargetPort: targetPort,
	}

	f := &activeForwarder{
		rule:     rule,
		listener: listener,
		stopCh:   make(chan struct{}),
	}
	pf.rules[key] = f

	go pf.tcpAcceptLoop(f)
	log.Printf("[orb-netd:forward] TCP proxy active: %s -> %s:%d", addr, targetIP, targetPort)
	return nil
}

func (pf *PortForwarder) tcpAcceptLoop(f *activeForwarder) {
	for {
		clientConn, err := f.listener.Accept()
		if err != nil {
			select {
			case <-f.stopCh:
				return
			case <-pf.stopCh:
				return
			default:
			}

			if atomic.LoadInt32(&f.closed) != 0 || atomic.LoadInt32(&pf.running) == 0 {
				return
			}

			// Transient accept error (e.g. EMFILE, ENFILE, ECONNABORTED, temporary net.Error)
			log.Printf("[orb-netd:forward] Transient accept error on %s:%d: %v", f.rule.HostIP, f.rule.HostPort, err)
			select {
			case <-time.After(15 * time.Millisecond):
				continue
			case <-f.stopCh:
				return
			case <-pf.stopCh:
				return
			}
		}

		go pf.proxyTCPConnection(clientConn, f.rule.TargetIP, f.rule.TargetPort)
	}
}

func (pf *PortForwarder) proxyTCPConnection(clientConn net.Conn, targetIP string, targetPort int) {
	defer clientConn.Close()

	targetAddr := fmt.Sprintf("%s:%d", targetIP, targetPort)
	targetConn, err := net.DialTimeout("tcp", targetAddr, 2*time.Second)
	if err != nil {
		log.Printf("[orb-netd:forward] Failed to dial backend %s: %v", targetAddr, err)
		return
	}
	defer targetConn.Close()

	var closeOnce sync.Once
	closeBoth := func() {
		closeOnce.Do(func() {
			_ = clientConn.Close()
			_ = targetConn.Close()
		})
	}
	defer closeBoth()

	type closeWriter interface {
		CloseWrite() error
	}

	var wg sync.WaitGroup
	wg.Add(2)

	// client -> target
	go func() {
		defer wg.Done()
		_, err := io.Copy(targetConn, clientConn)
		if err != nil {
			closeBoth()
			return
		}
		if cw, ok := targetConn.(closeWriter); ok {
			_ = cw.CloseWrite()
		}
	}()

	// target -> client
	go func() {
		defer wg.Done()
		_, err := io.Copy(clientConn, targetConn)
		if err != nil {
			closeBoth()
			return
		}
		if cw, ok := clientConn.(closeWriter); ok {
			_ = cw.CloseWrite()
		}
	}()

	wg.Wait()
}

// RemoveForward terminates a forwarding rule
func (pf *PortForwarder) RemoveForward(proto, hostIP string, hostPort int) error {
	if hostIP == "" {
		hostIP = "127.0.0.1"
	}
	key := fmt.Sprintf("%s:%s:%d", proto, hostIP, hostPort)

	pf.mu.Lock()
	defer pf.mu.Unlock()

	f, exists := pf.rules[key]
	if !exists {
		return fmt.Errorf("no forwarding rule found for %s", key)
	}

	atomic.StoreInt32(&f.closed, 1)
	close(f.stopCh)
	if f.listener != nil {
		_ = f.listener.Close()
	}
	if f.udpConn != nil {
		_ = f.udpConn.Close()
	}
	delete(pf.rules, key)
	log.Printf("[orb-netd:forward] Removed proxy: %s", key)
	return nil
}

// ListRules returns current forwarding rules
func (pf *PortForwarder) ListRules() []ForwardRule {
	pf.mu.RLock()
	defer pf.mu.RUnlock()

	rules := make([]ForwardRule, 0, len(pf.rules))
	for _, f := range pf.rules {
		rules = append(rules, f.rule)
	}
	return rules
}
