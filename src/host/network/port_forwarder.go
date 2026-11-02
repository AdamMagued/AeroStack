package main

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"strconv"
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

	// 1. Try direct TCP connection with a quick timeout (succeeds for host-to-host or accessible networks)
	targetAddr := fmt.Sprintf("%s:%d", targetIP, targetPort)
	dialTimeout := 200 * time.Millisecond
	if targetIP == "127.0.0.1" || targetIP == "localhost" {
		dialTimeout = 2 * time.Second
	}
	targetConn, err := net.DialTimeout("tcp", targetAddr, dialTimeout)
	if err == nil {
		defer targetConn.Close()
		pf.spliceTCP(clientConn, targetConn)
		return
	}

	// 2. Fallback to AeroStack vsock exec bridge when direct NAT connection fails
	if err := pf.proxyViaExecBridge(clientConn, targetPort); err != nil {
		log.Printf("[orb-netd:forward] Failed to proxy %s:%d (direct dial: %v, vsock: %v)", targetIP, targetPort, err, err)
	}
}

func (pf *PortForwarder) spliceTCP(clientConn, targetConn net.Conn) {
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

func (pf *PortForwarder) proxyViaExecBridge(clientConn net.Conn, targetPort int) error {
	home, _ := os.UserHomeDir()
	execSock := filepath.Join(home, ".aerostack", "run", "exec.sock")
	var execConn net.Conn
	var err error

	if _, statErr := os.Stat(execSock); statErr == nil {
		execConn, err = net.DialTimeout("unix", execSock, 2*time.Second)
	} else {
		execConn, err = net.DialTimeout("tcp", "127.0.0.1:8022", 2*time.Second)
	}
	if err != nil {
		return fmt.Errorf("connect to exec bridge failed: %w", err)
	}
	defer execConn.Close()

	var closeOnce sync.Once
	closeBoth := func() {
		closeOnce.Do(func() {
			_ = clientConn.Close()
			_ = execConn.Close()
		})
	}
	defer closeBoth()

	// Prepare ExecRequest for "nc 127.0.0.1 <targetPort>" inside guest
	req := map[string]interface{}{
		"command": []string{"nc", "127.0.0.1", strconv.Itoa(targetPort)},
		"tty":     false,
	}
	reqBytes, err := json.Marshal(req)
	if err != nil {
		return err
	}

	// Send ExecMsgRequest (type 1)
	hdr := make([]byte, 5)
	hdr[0] = 1 // ExecMsgRequest
	binary.BigEndian.PutUint32(hdr[1:], uint32(len(reqBytes)))
	if _, err := execConn.Write(hdr); err != nil {
		return err
	}
	if _, err := execConn.Write(reqBytes); err != nil {
		return err
	}

	// Read ExecResponse
	respHdr := make([]byte, 5)
	if _, err := io.ReadFull(execConn, respHdr); err != nil {
		return err
	}
	respLen := binary.BigEndian.Uint32(respHdr[1:])
	respBody := make([]byte, respLen)
	if _, err := io.ReadFull(execConn, respBody); err != nil {
		return err
	}
	var initResp struct {
		Started bool   `json:"started"`
		Error   string `json:"error,omitempty"`
	}
	if err := json.Unmarshal(respBody, &initResp); err == nil && !initResp.Started && initResp.Error != "" {
		return fmt.Errorf("remote exec init error: %s", initResp.Error)
	}

	var wg sync.WaitGroup
	wg.Add(2)

	// Pump clientConn -> execConn (wrapping data in ExecMsgDataStdin frames)
	go func() {
		defer wg.Done()
		buf := make([]byte, 8192)
		for {
			n, rerr := clientConn.Read(buf)
			if n > 0 {
				inHdr := make([]byte, 5+n)
				inHdr[0] = 3 // ExecMsgDataStdin
				binary.BigEndian.PutUint32(inHdr[1:], uint32(n))
				copy(inHdr[5:], buf[:n])
				if _, werr := execConn.Write(inHdr); werr != nil {
					closeBoth()
					return
				}
			}
			if rerr != nil {
				// Send EOF frame
				eofHdr := make([]byte, 5)
				eofHdr[0] = 8 // ExecMsgStdinEOF
				binary.BigEndian.PutUint32(eofHdr[1:], 0)
				_, _ = execConn.Write(eofHdr)
				return
			}
		}
	}()

	// Pump execConn -> clientConn (unwrapping ExecMsgDataStdout frames)
	go func() {
		defer wg.Done()
		fHdr := make([]byte, 5)
		for {
			if _, err := io.ReadFull(execConn, fHdr); err != nil {
				break
			}
			mType := fHdr[0]
			pLen := binary.BigEndian.Uint32(fHdr[1:])
			pBuf := make([]byte, pLen)
			if _, err := io.ReadFull(execConn, pBuf); err != nil {
				break
			}
			if mType == 4 { // ExecMsgDataStdout
				if _, err := clientConn.Write(pBuf); err != nil {
					break
				}
			} else if mType == 7 { // ExecMsgExitStatus
				break
			}
		}
		closeBoth()
	}()

	wg.Wait()
	return nil
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
