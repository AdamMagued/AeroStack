package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"time"
)

func runProxyBenchmarks() {
	fmt.Println("\n================================================================================")
	fmt.Println("   ORBSTACK FREE: DOCKER ENGINE API PROXY LATENCY BENCHMARK")
	fmt.Println("================================================================================")

	tmpDir, _ := os.MkdirTemp("/tmp", "proxy-bench-*")
	defer os.RemoveAll(tmpDir)

	backendSocket := filepath.Join(tmpDir, "backend.sock")
	proxySocket := filepath.Join(tmpDir, "proxy.sock")

	// 1. Mock Backend Listener
	backendListener, err := net.Listen("unix", backendSocket)
	if err != nil {
		fmt.Printf("failed to listen on backend: %v\n", err)
		return
	}
	defer backendListener.Close()

	go func() {
		for {
			conn, err := backendListener.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				buf := make([]byte, 1024)
				for {
					n, err := c.Read(buf)
					if err != nil || n == 0 {
						return
					}
					// Return fast 200 OK
					resp := "HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\nContent-Length: 2\r\n\r\nOK"
					_, _ = c.Write([]byte(resp))
				}
			}(conn)
		}
	}()

	// 2. Simple Zero-Copy Unix Socket Proxy Bridge
	proxyListener, err := net.Listen("unix", proxySocket)
	if err != nil {
		fmt.Printf("failed to listen on proxy: %v\n", err)
		return
	}
	defer proxyListener.Close()

	go func() {
		for {
			clientConn, err := proxyListener.Accept()
			if err != nil {
				return
			}
			go func(cc net.Conn) {
				defer cc.Close()
				bc, err := net.Dial("unix", backendSocket)
				if err != nil {
					return
				}
				defer bc.Close()

				// Bidirectional splice
				go func() {
					_, _ = io.Copy(bc, cc)
				}()
				_, _ = io.Copy(cc, bc)
			}(clientConn)
		}
	}()

	time.Sleep(50 * time.Millisecond)

	// Measure Direct Latency
	iterations := 1000
	directLatencies := make([]time.Duration, iterations)
	directClient := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return net.Dial("unix", backendSocket)
			},
		},
	}
	defer directClient.CloseIdleConnections()

	// Warmup
	_, _ = directClient.Get("http://docker/ping")

	for i := 0; i < iterations; i++ {
		t0 := time.Now()
		resp, err := directClient.Get("http://docker/ping")
		if err == nil {
			_, _ = io.ReadAll(resp.Body)
			resp.Body.Close()
		}
		directLatencies[i] = time.Since(t0)
	}

	// Measure Proxied Latency
	proxiedLatencies := make([]time.Duration, iterations)
	proxiedClient := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return net.Dial("unix", proxySocket)
			},
		},
	}
	defer proxiedClient.CloseIdleConnections()

	// Warmup
	_, _ = proxiedClient.Get("http://docker/ping")

	for i := 0; i < iterations; i++ {
		t0 := time.Now()
		resp, err := proxiedClient.Get("http://docker/ping")
		if err == nil {
			_, _ = io.ReadAll(resp.Body)
			resp.Body.Close()
		}
		proxiedLatencies[i] = time.Since(t0)
	}

	sort.Slice(directLatencies, func(i, j int) bool { return directLatencies[i] < directLatencies[j] })
	sort.Slice(proxiedLatencies, func(i, j int) bool { return proxiedLatencies[i] < proxiedLatencies[j] })

	directP50 := float64(directLatencies[iterations/2].Microseconds()) / 1000.0
	directP99 := float64(directLatencies[int(float64(iterations)*0.99)].Microseconds()) / 1000.0

	proxyP50 := float64(proxiedLatencies[iterations/2].Microseconds()) / 1000.0
	proxyP99 := float64(proxiedLatencies[int(float64(iterations)*0.99)].Microseconds()) / 1000.0

	overheadP50 := proxyP50 - directP50
	overheadP99 := proxyP99 - directP99

	fmt.Printf("Iterations: %d HTTP requests over Unix Domain Socket\n", iterations)
	fmt.Printf("Direct Socket:   P50: %6.3f ms | P99: %6.3f ms\n", directP50, directP99)
	fmt.Printf("Proxied Socket:  P50: %6.3f ms | P99: %6.3f ms\n", proxyP50, proxyP99)
	fmt.Printf("Proxy Overhead:  P50: +%5.3f ms | P99: +%5.3f ms\n", overheadP50, overheadP99)
}
