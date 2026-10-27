package main

import (
	"bufio"
	"io"
	"net"
	"strings"
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
func SpliceHijackedStream(clientConn net.Conn, clientBuf *bufio.Reader,
	backendConn net.Conn, backendBuf *bufio.Reader) (*StreamStats, error) {

	stats := &StreamStats{
		StartTime: time.Now(),
	}

	var bytesC2B int64
	var bytesB2C int64

	var wg sync.WaitGroup
	wg.Add(2)

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
				}
			}
		}

		// Stream remaining client data to backend
		var r io.Reader = clientConn
		if clientBuf != nil {
			r = clientBuf
		}

		n, _ := io.Copy(backendConn, r)
		atomic.AddInt64(&bytesC2B, n)

		// Signal EOF to backend
		if closeWriter, ok := backendConn.(interface{ CloseWrite() error }); ok {
			_ = closeWriter.CloseWrite()
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
				}
			}
		}

		// Stream remaining backend data to client
		var r io.Reader = backendConn
		if backendBuf != nil {
			r = backendBuf
		}

		n, _ := io.Copy(clientConn, r)
		atomic.AddInt64(&bytesB2C, n)

		// Signal EOF to client
		if closeWriter, ok := clientConn.(interface{ CloseWrite() error }); ok {
			_ = closeWriter.CloseWrite()
		}
	}()

	wg.Wait()
	stats.Duration = time.Since(stats.StartTime)
	stats.BytesClientToBackend = atomic.LoadInt64(&bytesC2B)
	stats.BytesBackendToClient = atomic.LoadInt64(&bytesB2C)

	return stats, nil
}

func stringsEqualFold(s1, s2 string) bool {
	return strings.EqualFold(s1, s2)
}

func stringsContainsFold(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}

func isClosedConnError(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "use of closed network connection") ||
		strings.Contains(s, "connection reset by peer") ||
		strings.Contains(s, "broken pipe")
}
