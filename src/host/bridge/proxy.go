package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DockerProxyConfig holds proxy configuration parameters
type DockerProxyConfig struct {
	ListenSocketPaths []string
	BackendTransport  TransportConfig
	GuestMountPrefix  string
	LogLevel          string
}

// DockerProxy is the high-performance Unix socket proxy for Docker Engine API
type DockerProxy struct {
	config      DockerProxyConfig
	translator  *PathTranslator
	dialer      BackendDialer
	listeners   []net.Listener
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

// Start begins listening on all configured Unix domain sockets
func (p *DockerProxy) Start() error {
	for _, sockPath := range p.config.ListenSocketPaths {
		if sockPath == "" {
			continue
		}

		dir := filepath.Dir(sockPath)
		if err := os.MkdirAll(dir, 0755); err != nil {
			// If permission error on /var/run, log and continue for user socket
			log.Printf("[docker-proxy:warn] Failed to create socket directory %s: %v", dir, err)
			continue
		}

		_ = os.Remove(sockPath)
		l, err := net.Listen("unix", sockPath)
		if err != nil {
			log.Printf("[docker-proxy:warn] Failed to listen on socket %s: %v", sockPath, err)
			continue
		}

		_ = os.Chmod(sockPath, 0666)
		p.listeners = append(p.listeners, l)
		log.Printf("[docker-proxy] Listening on unix://%s -> backend %s", sockPath, p.dialer)

		p.wg.Add(1)
		go p.acceptLoop(l)
	}

	if len(p.listeners) == 0 {
		return fmt.Errorf("failed to bind to any listen sockets (configured: %v)", p.config.ListenSocketPaths)
	}

	return nil
}

// Stop gracefully terminates the proxy
func (p *DockerProxy) Stop() error {
	p.cancel()
	for _, l := range p.listeners {
		if l != nil {
			_ = l.Close()
		}
	}
	for _, sockPath := range p.config.ListenSocketPaths {
		_ = os.Remove(sockPath)
	}

	p.activeMu.Lock()
	for c := range p.activeConns {
		_ = c.Close()
	}
	p.activeMu.Unlock()

	p.wg.Wait()
	log.Printf("[docker-proxy] Stopped cleanly")
	return nil
}

func (p *DockerProxy) acceptLoop(l net.Listener) {
	defer p.wg.Done()

	for {
		clientConn, err := l.Accept()
		if err != nil {
			select {
			case <-p.ctx.Done():
				return
			default:
				if errors.Is(err, net.ErrClosed) || strings.Contains(err.Error(), "use of closed network connection") {
					return
				}
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

func (p *DockerProxy) handleClient(clientConn net.Conn) {
	p.activeMu.Lock()
	p.activeConns[clientConn] = struct{}{}
	p.activeMu.Unlock()

	defer func() {
		p.activeMu.Lock()
		delete(p.activeConns, clientConn)
		p.activeMu.Unlock()
		_ = clientConn.Close()
	}()

	clientBuf := bufio.NewReader(clientConn)

	// Connect to backend
	backendConn, err := p.dialer.Dial(p.ctx)
	if err != nil {
		resp := "HTTP/1.1 502 Bad Gateway\r\nContent-Type: text/plain\r\nConnection: close\r\n\r\nFailed to connect to guest Docker daemon\n"
		_, _ = clientConn.Write([]byte(resp))
		return
	}
	defer backendConn.Close()

	backendBuf := bufio.NewReader(backendConn)

	// HTTP Keep-Alive loop
	for {
		_ = clientConn.SetReadDeadline(time.Now().Add(60 * time.Minute))

		req, err := http.ReadRequest(clientBuf)
		if err != nil {
			return
		}
		_ = clientConn.SetReadDeadline(time.Time{})

		path := req.URL.Path
		method := req.Method

		// Check for hijacked streaming request
		if IsHijackRequest(method, req.URL.RequestURI(), req.Header) {
			p.handleHijackedStream(clientConn, clientBuf, backendConn, backendBuf, req)
			return
		}

		// Read request body
		var reqBodyBytes []byte
		if req.Body != nil {
			reqBodyBytes, err = io.ReadAll(req.Body)
			_ = req.Body.Close()
			if err != nil {
				return
			}
		}

		// Intercept Container Create: rewrite host paths to VirtioFS paths
		if method == http.MethodPost && (strings.HasSuffix(path, "/containers/create") || path == "/containers/create") {
			translated, terr := p.translator.TranslateContainerCreateRequestBody(reqBodyBytes)
			if terr == nil {
				reqBodyBytes = translated
				req.Header.Set("Content-Length", strconv.Itoa(len(reqBodyBytes)))
			}
		}

		req.Body = io.NopCloser(bytes.NewReader(reqBodyBytes))
		req.ContentLength = int64(len(reqBodyBytes))

		// Write modified request to backend
		if err := req.Write(backendConn); err != nil {
			return
		}

		// Read response from backend
		resp, err := http.ReadResponse(backendBuf, req)
		if err != nil {
			return
		}

		// Check if backend responded with 101 UPGRADED
		if resp.StatusCode == http.StatusSwitchingProtocols || IsHijackResponse(resp.StatusCode, resp.Header) {
			_ = resp.Write(clientConn)
			_, _ = SpliceHijackedStream(clientConn, clientBuf, backendConn, backendBuf)
			return
		}

		// Intercept Container Inspect: reverse translate guest paths back to macOS paths
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

		// Write response back to client
		if err := resp.Write(clientConn); err != nil {
			return
		}

		if resp.Close {
			return
		}
	}
}

func (p *DockerProxy) handleHijackedStream(clientConn net.Conn, clientBuf *bufio.Reader,
	backendConn net.Conn, backendBuf *bufio.Reader, req *http.Request) {

	// Forward hijack request directly to backend
	if err := req.Write(backendConn); err != nil {
		return
	}

	// Read backend response (expected 101 UPGRADED or 200 raw stream)
	resp, err := http.ReadResponse(backendBuf, req)
	if err != nil {
		return
	}

	// Write handshake response to client
	if err := resp.Write(clientConn); err != nil {
		return
	}

	// Enter zero-copy bidirectional splicing
	_, _ = SpliceHijackedStream(clientConn, clientBuf, backendConn, backendBuf)
}
