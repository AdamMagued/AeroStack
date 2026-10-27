package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DockerProxyConfig holds proxy configuration parameters
type DockerProxyConfig struct {
	ListenSocketPath string
	BackendTransport TransportConfig
	GuestMountPrefix string
	LogLevel         string
}

// DockerProxy is the high-performance Unix socket proxy for Docker Engine API
type DockerProxy struct {
	config      DockerProxyConfig
	translator  *PathTranslator
	dialer      BackendDialer
	listener    net.Listener
	ctx         context.Context
	cancel      context.CancelFunc
	wg          sync.WaitGroup
	activeMu    sync.Mutex
	activeConns map[net.Conn]struct{}
}

// NewDockerProxy constructs a new proxy instance
func NewDockerProxy(cfg DockerProxyConfig) (*DockerProxy, error) {
	dialer, err := CreateDialer(cfg.BackendTransport)
	if err != nil {
		return nil, fmt.Errorf("failed to create backend dialer: %w", err)
	}

	translator := NewPathTranslator(cfg.GuestMountPrefix)
	ctx, cancel := context.WithCancel(context.Background())

	return &DockerProxy{
		config:      cfg,
		translator:  translator,
		dialer:      dialer,
		ctx:         ctx,
		cancel:      cancel,
		activeConns: make(map[net.Conn]struct{}),
	}, nil
}

// Start begins listening on the Unix domain socket and serving connections
func (p *DockerProxy) Start() error {
	// Ensure parent directory exists
	dir := filepath.Dir(p.config.ListenSocketPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create socket directory %s: %w", dir, err)
	}

	// Remove stale socket if exists
	_ = os.Remove(p.config.ListenSocketPath)

	l, err := net.Listen("unix", p.config.ListenSocketPath)
	if err != nil {
		return fmt.Errorf("failed to listen on socket %s: %w", p.config.ListenSocketPath, err)
	}

	// Allow current user and staff/admin group access
	_ = os.Chmod(p.config.ListenSocketPath, 0666)

	p.listener = l
	log.Printf("[docker-proxy] Listening on unix://%s -> backend %s", p.config.ListenSocketPath, p.dialer)

	p.wg.Add(1)
	go p.acceptLoop()

	return nil
}

// Stop gracefully terminates the proxy
func (p *DockerProxy) Stop() error {
	p.cancel()
	if p.listener != nil {
		_ = p.listener.Close()
	}
	_ = os.Remove(p.config.ListenSocketPath)

	// Close all active connections so goroutines exit immediately
	p.activeMu.Lock()
	for c := range p.activeConns {
		_ = c.Close()
	}
	p.activeMu.Unlock()

	p.wg.Wait()
	log.Printf("[docker-proxy] Stopped cleanly")
	return nil
}

// acceptLoop accepts client connections
func (p *DockerProxy) acceptLoop() {
	defer p.wg.Done()

	for {
		clientConn, err := p.listener.Accept()
		if err != nil {
			select {
			case <-p.ctx.Done():
				return
			default:
				log.Printf("[docker-proxy] accept error: %v", err)
				time.Sleep(10 * time.Millisecond)
				continue
			}
		}

		p.wg.Add(1)
		go func(conn net.Conn) {
			defer p.wg.Done()
			p.handleClient(conn)
		}(clientConn)
	}
}

// handleClient handles one client connection lifecycle
func (p *DockerProxy) handleClient(clientConn net.Conn) {
	p.activeMu.Lock()
	p.activeConns[clientConn] = struct{}{}
	p.activeMu.Unlock()

	defer func() {
		p.activeMu.Lock()
		delete(p.activeConns, clientConn)
		p.activeMu.Unlock()
		clientConn.Close()
	}()

	clientBuf := bufio.NewReader(clientConn)

	// Dial backend
	backendConn, err := p.dialer.Dial(p.ctx)
	if err != nil {
		log.Printf("[docker-proxy] dial backend failed: %v", err)
		resp := "HTTP/1.1 502 Bad Gateway\r\nContent-Type: text/plain\r\nConnection: close\r\n\r\nFailed to connect to guest Docker daemon\n"
		_, _ = clientConn.Write([]byte(resp))
		return
	}
	defer backendConn.Close()

	backendBuf := bufio.NewReader(backendConn)

	// Process HTTP requests in loop (HTTP Keep-Alive)
	for {
		// Set read timeout for request header
		_ = clientConn.SetReadDeadline(time.Now().Add(60 * time.Minute))

		req, err := http.ReadRequest(clientBuf)
		if err != nil {
			if err != io.EOF && !isClosedConnError(err) {
				// Normal disconnect
			}
			return
		}

		// Disable deadline for active processing
		_ = clientConn.SetReadDeadline(time.Time{})

		path := req.URL.Path
		method := req.Method

		// Check if request is a Stream Hijack (exec -it, attach, etc.)
		if IsHijackRequest(method, req.URL.RequestURI(), req.Header) {
			p.handleHijackedStream(clientConn, clientBuf, backendConn, backendBuf, req)
			return // Once hijacked, connection ownership is ceded
		}

		// Handle request rewriting
		var reqBodyBytes []byte
		if req.Body != nil {
			reqBodyBytes, err = io.ReadAll(req.Body)
			_ = req.Body.Close()
			if err != nil {
				log.Printf("[docker-proxy] read request body error: %v", err)
				return
			}
		}

		// Intercept Container Create: rewrite host paths to VirtioFS paths
		if method == http.MethodPost && (strings.HasSuffix(path, "/containers/create") || path == "/containers/create") {
			translated, terr := p.translator.TranslateContainerCreateRequestBody(reqBodyBytes)
			if terr == nil {
				reqBodyBytes = translated
				req.Header.Set("Content-Length", strconv.Itoa(len(reqBodyBytes)))
			} else {
				log.Printf("[docker-proxy] translation error on container create: %v", terr)
			}
		}

		// Prepare request for forwarding
		req.Body = io.NopCloser(bytes.NewReader(reqBodyBytes))
		req.ContentLength = int64(len(reqBodyBytes))

		// Write modified request to backend
		if err := req.Write(backendConn); err != nil {
			log.Printf("[docker-proxy] forward request error: %v", err)
			return
		}

		// Read response from backend
		resp, err := http.ReadResponse(backendBuf, req)
		if err != nil {
			log.Printf("[docker-proxy] read response error: %v", err)
			return
		}

		// Check if backend responded with 101 UPGRADED
		if resp.StatusCode == http.StatusSwitchingProtocols || IsHijackResponse(resp.StatusCode, resp.Header) {
			_ = resp.Write(clientConn)
			_, _ = SpliceHijackedStream(clientConn, clientBuf, backendConn, backendBuf)
			return
		}

		// Intercept Container Inspect: rewrite VirtioFS paths back to host paths
		isInspect := method == http.MethodGet && (strings.HasSuffix(path, "/json") && strings.Contains(path, "/containers/"))
		if isInspect && resp.StatusCode == http.StatusOK {
			respBodyBytes, rerr := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if rerr == nil {
				translatedResp, terr := p.translator.TranslateContainerInspectResponseBody(respBodyBytes)
				if terr == nil {
					respBodyBytes = translatedResp
				}
				resp.Body = io.NopCloser(bytes.NewReader(respBodyBytes))
				resp.ContentLength = int64(len(respBodyBytes))
				resp.Header.Set("Content-Length", strconv.Itoa(len(respBodyBytes)))
			}
		}

		// Forward response to client (supports chunked streaming for builds/pulls/events)
		if err := resp.Write(clientConn); err != nil {
			_ = resp.Body.Close()
			return
		}
		_ = resp.Body.Close()

		// If connection close requested
		if req.Close || resp.Close {
			return
		}
	}
}

// handleHijackedStream forwards the initial HTTP upgrade request and immediately switches to raw duplex splice
func (p *DockerProxy) handleHijackedStream(clientConn net.Conn, clientBuf *bufio.Reader,
	backendConn net.Conn, backendBuf *bufio.Reader, req *http.Request) {

	// Forward raw HTTP request to backend
	dump, err := httputil.DumpRequest(req, true)
	if err != nil {
		log.Printf("[docker-proxy] dump hijack request error: %v", err)
		return
	}

	if _, err := backendConn.Write(dump); err != nil {
		log.Printf("[docker-proxy] write hijack request error: %v", err)
		return
	}

	// Read backend response header
	resp, err := http.ReadResponse(backendBuf, req)
	if err != nil {
		log.Printf("[docker-proxy] read hijack response error: %v", err)
		return
	}

	// Write response headers to client
	if err := resp.Write(clientConn); err != nil {
		log.Printf("[docker-proxy] write hijack response to client error: %v", err)
		return
	}

	// Enter raw stream splicing
	stats, err := SpliceHijackedStream(clientConn, clientBuf, backendConn, backendBuf)
	if err != nil {
		log.Printf("[docker-proxy] hijacked stream ended with err: %v (tx: %d bytes, rx: %d bytes, dur: %v)",
			err, stats.BytesClientToBackend, stats.BytesBackendToClient, stats.Duration)
	} else {
		log.Printf("[docker-proxy] hijacked stream closed cleanly (tx: %d bytes, rx: %d bytes, dur: %v)",
			stats.BytesClientToBackend, stats.BytesBackendToClient, stats.Duration)
	}
}
