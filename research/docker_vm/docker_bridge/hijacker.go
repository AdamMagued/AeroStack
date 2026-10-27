package main

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// StreamStats records metrics about hijacked stream throughput and duration
type StreamStats struct {
	BytesClientToBackend int64
	BytesBackendToClient int64
	Duration             time.Duration
	StartTime            time.Time
}

// IsHijackRequest inspects request headers and URL to detect if it is a hijacked stream
func IsHijackRequest(method, uri string, headers map[string][]string) bool {
	// 1. Check Upgrade header
	for k, vals := range headers {
		if stringsEqualFold(k, "Upgrade") {
			for _, v := range vals {
				if stringsEqualFold(v, "tcp") {
					return true
				}
			}
		}
		if stringsEqualFold(k, "Connection") {
			for _, v := range vals {
				if stringsContainsFold(v, "Upgrade") {
					return true
				}
			}
		}
	}

	// 2. Check attach or exec start endpoints with stream=1
	if stringsContainsFold(uri, "/attach") || stringsContainsFold(uri, "/exec/") {
		if stringsContainsFold(uri, "stream=1") || stringsContainsFold(uri, "stream=true") {
			return true
		}
	}

	return false
}

// IsHijackResponse checks if HTTP response status is 101 UPGRADED or raw-stream
func IsHijackResponse(statusCode int, headers map[string][]string) bool {
	if statusCode == 101 {
		return true
	}
	for k, vals := range headers {
		if stringsEqualFold(k, "Content-Type") {
			for _, v := range vals {
				if stringsContainsFold(v, "application/vnd.docker.raw-stream") {
					return true
				}
			}
		}
	}
	return false
}

// SpliceHijackedStream manages bidirectional raw full-duplex forwarding between client and backend.
// It handles buffered data remaining in bufio.Reader on either side.
func SpliceHijackedStream(clientConn net.Conn, clientBuf *bufio.Reader,
	backendConn net.Conn, backendBuf *bufio.Reader) (*StreamStats, error) {

	stats := &StreamStats{
		StartTime: time.Now(),
	}

	var bytesC2B int64
	var bytesB2C int64

	var wg sync.WaitGroup
	wg.Add(2)

	errChan := make(chan error, 2)

	// Goroutine 1: Client -> Backend (stdin)
	go func() {
		defer wg.Done()

		// First flush any pre-buffered client bytes
		if clientBuf != nil && clientBuf.Buffered() > 0 {
			n := clientBuf.Buffered()
			buf := make([]byte, n)
			if _, err := io.ReadFull(clientBuf, buf); err == nil {
				if wn, werr := backendConn.Write(buf); werr == nil {
					atomic.AddInt64(&bytesC2B, int64(wn))
				} else {
					errChan <- fmt.Errorf("error flushing client buffer: %w", werr)
					return
				}
			}
		}

		// Stream remaining client data to backend
		var r io.Reader = clientConn
		if clientBuf != nil {
			r = clientBuf
		}

		n, err := io.Copy(backendConn, r)
		atomic.AddInt64(&bytesC2B, n)

		// Client is done sending data, signal EOF to backend
		if closeWriter, ok := backendConn.(interface{ CloseWrite() error }); ok {
			_ = closeWriter.CloseWrite()
		} else if tcpConn, ok := backendConn.(*net.TCPConn); ok {
			_ = tcpConn.CloseWrite()
		} else if unixConn, ok := backendConn.(*net.UnixConn); ok {
			_ = unixConn.CloseWrite()
		}

		if err != nil && err != io.EOF && !isClosedConnError(err) {
			errChan <- fmt.Errorf("c2b error: %w", err)
		}
	}()

	// Goroutine 2: Backend -> Client (stdout/stderr)
	go func() {
		defer wg.Done()

		// First flush any pre-buffered backend bytes
		if backendBuf != nil && backendBuf.Buffered() > 0 {
			n := backendBuf.Buffered()
			buf := make([]byte, n)
			if _, err := io.ReadFull(backendBuf, buf); err == nil {
				if wn, werr := clientConn.Write(buf); werr == nil {
					atomic.AddInt64(&bytesB2C, int64(wn))
				} else {
					errChan <- fmt.Errorf("error flushing backend buffer: %w", werr)
					return
				}
			}
		}

		// Stream remaining backend data to client
		var r io.Reader = backendConn
		if backendBuf != nil {
			r = backendBuf
		}

		n, err := io.Copy(clientConn, r)
		atomic.AddInt64(&bytesB2C, n)

		// Backend finished sending output, signal EOF to client
		if closeWriter, ok := clientConn.(interface{ CloseWrite() error }); ok {
			_ = closeWriter.CloseWrite()
		} else if tcpConn, ok := clientConn.(*net.TCPConn); ok {
			_ = tcpConn.CloseWrite()
		} else if unixConn, ok := clientConn.(*net.UnixConn); ok {
			_ = unixConn.CloseWrite()
		}

		if err != nil && err != io.EOF && !isClosedConnError(err) {
			errChan <- fmt.Errorf("b2c error: %w", err)
		}
	}()

	// Wait for both directions to complete
	wg.Wait()
	close(errChan)

	stats.Duration = time.Since(stats.StartTime)
	stats.BytesClientToBackend = atomic.LoadInt64(&bytesC2B)
	stats.BytesBackendToClient = atomic.LoadInt64(&bytesB2C)

	// Collect any fatal error
	var finalErr error
	for err := range errChan {
		if finalErr == nil {
			finalErr = err
		}
	}

	return stats, finalErr
}

// Helpers
func stringsEqualFold(s1, s2 string) bool {
	if len(s1) != len(s2) {
		return false
	}
	for i := 0; i < len(s1); i++ {
		c1 := s1[i]
		c2 := s2[i]
		if c1 >= 'A' && c1 <= 'Z' {
			c1 += 'a' - 'A'
		}
		if c2 >= 'A' && c2 <= 'Z' {
			c2 += 'a' - 'A'
		}
		if c1 != c2 {
			return false
		}
	}
	return true
}

func stringsContainsFold(s, substr string) bool {
	sLower := []byte(s)
	for i := range sLower {
		if sLower[i] >= 'A' && sLower[i] <= 'Z' {
			sLower[i] += 'a' - 'A'
		}
	}
	subLower := []byte(substr)
	for i := range subLower {
		if subLower[i] >= 'A' && subLower[i] <= 'Z' {
			subLower[i] += 'a' - 'A'
		}
	}

	// simple substring check
	n := len(subLower)
	if n == 0 {
		return true
	}
	if len(sLower) < n {
		return false
	}
	for i := 0; i <= len(sLower)-n; i++ {
		match := true
		for j := 0; j < n; j++ {
			if sLower[i+j] != subLower[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

func isClosedConnError(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return stringsContainsFold(s, "use of closed network connection") ||
		stringsContainsFold(s, "broken pipe") ||
		stringsContainsFold(s, "connection reset by peer")
}
