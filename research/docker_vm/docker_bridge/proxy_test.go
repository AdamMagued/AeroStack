package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestPathTranslator verifies bidirectional path translation logic
func TestPathTranslator(t *testing.T) {
	pt := NewPathTranslator("/mnt/mac")
	pt.AddMapping("/Volumes/AdamSSD/workspace", "/mnt/adamssd/workspace")

	// 1. Standard /Users path translation
	hostUserPath := "/Users/adammaged/code/app"
	guestUserPath := pt.HostToGuest(hostUserPath)
	expectedGuest := "/mnt/mac/Users/adammaged/code/app"
	if guestUserPath != expectedGuest {
		t.Fatalf("expected %s, got %s", expectedGuest, guestUserPath)
	}

	// Reverse
	revHost := pt.GuestToHost(guestUserPath)
	if revHost != hostUserPath {
		t.Fatalf("reverse expected %s, got %s", hostUserPath, revHost)
	}

	// 2. Custom mapped path
	customHost := "/Volumes/AdamSSD/workspace/project1"
	customGuest := pt.HostToGuest(customHost)
	expectedCustomGuest := "/mnt/adamssd/workspace/project1"
	if customGuest != expectedCustomGuest {
		t.Fatalf("expected %s, got %s", expectedCustomGuest, customGuest)
	}

	revCustomHost := pt.GuestToHost(customGuest)
	if revCustomHost != customHost {
		t.Fatalf("expected %s, got %s", customHost, revCustomHost)
	}

	// 3. Special docker socket path (must NOT be translated)
	sockPath := "/var/run/docker.sock"
	if pt.HostToGuest(sockPath) != sockPath {
		t.Fatalf("docker.sock was improperly translated")
	}
}

// TestContainerCreateTranslation verifies JSON rewriting on POST /containers/create
func TestContainerCreateTranslation(t *testing.T) {
	pt := NewPathTranslator("/mnt/mac")

	createReq := map[string]interface{}{
		"Image": "alpine:latest",
		"HostConfig": map[string]interface{}{
			"Binds": []interface{}{
				"/Users/alice/app:/app:ro",
				"/var/run/docker.sock:/var/run/docker.sock",
			},
			"Mounts": []interface{}{
				map[string]interface{}{
					"Type":   "bind",
					"Source": "/Users/alice/data",
					"Target": "/data",
				},
				map[string]interface{}{
					"Type":   "volume",
					"Source": "my-named-vol",
					"Target": "/vol",
				},
			},
		},
	}

	rawJSON, _ := json.Marshal(createReq)
	translatedBytes, err := pt.TranslateContainerCreateRequestBody(rawJSON)
	if err != nil {
		t.Fatalf("translation failed: %v", err)
	}

	var parsed map[string]interface{}
	_ = json.Unmarshal(translatedBytes, &parsed)

	hc := parsed["HostConfig"].(map[string]interface{})
	binds := hc["Binds"].([]interface{})
	if binds[0].(string) != "/mnt/mac/Users/alice/app:/app:ro" {
		t.Fatalf("expected rewritten bind, got: %s", binds[0])
	}
	if binds[1].(string) != "/var/run/docker.sock:/var/run/docker.sock" {
		t.Fatalf("special socket should remain unchanged, got: %s", binds[1])
	}

	mounts := hc["Mounts"].([]interface{})
	m0 := mounts[0].(map[string]interface{})
	if m0["Source"].(string) != "/mnt/mac/Users/alice/data" {
		t.Fatalf("expected rewritten mount source, got: %s", m0["Source"])
	}
	m1 := mounts[1].(map[string]interface{})
	if m1["Source"].(string) != "my-named-vol" {
		t.Fatalf("named volume source should not change, got: %s", m1["Source"])
	}
}

// TestContainerInspectTranslation verifies JSON reverse rewriting on GET /containers/{id}/json
func TestContainerInspectTranslation(t *testing.T) {
	pt := NewPathTranslator("/mnt/mac")

	inspectResp := map[string]interface{}{
		"Id": "abc123456",
		"Mounts": []interface{}{
			map[string]interface{}{
				"Type":        "bind",
				"Source":      "/mnt/mac/Users/alice/data",
				"Destination": "/data",
			},
		},
		"HostConfig": map[string]interface{}{
			"Binds": []interface{}{
				"/mnt/mac/Users/alice/app:/app:ro",
			},
		},
	}

	rawJSON, _ := json.Marshal(inspectResp)
	rewrittenBytes, err := pt.TranslateContainerInspectResponseBody(rawJSON)
	if err != nil {
		t.Fatalf("reverse translation failed: %v", err)
	}

	var parsed map[string]interface{}
	_ = json.Unmarshal(rewrittenBytes, &parsed)

	mounts := parsed["Mounts"].([]interface{})
	m0 := mounts[0].(map[string]interface{})
	if m0["Source"].(string) != "/Users/alice/data" {
		t.Fatalf("expected reverse translated source, got: %s", m0["Source"])
	}

	hc := parsed["HostConfig"].(map[string]interface{})
	binds := hc["Binds"].([]interface{})
	if binds[0].(string) != "/Users/alice/app:/app:ro" {
		t.Fatalf("expected reverse translated bind, got: %s", binds[0])
	}
}

// TestEndToEndProxyWithStreamHijack spins up a mock dockerd backend and tests full proxy lifecycle
func TestEndToEndProxyWithStreamHijack(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "docker-proxy-test-*")
	if err != nil {
		t.Fatalf("temp dir error: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	mockBackendSocket := filepath.Join(tmpDir, "backend_dockerd.sock")
	proxySocket := filepath.Join(tmpDir, "proxy_docker.sock")

	// 1. Mock Dockerd Backend
	mockBackendListener, err := net.Listen("unix", mockBackendSocket)
	if err != nil {
		t.Fatalf("failed to listen on mock backend: %v", err)
	}
	defer mockBackendListener.Close()

	go func() {
		for {
			conn, err := mockBackendListener.Accept()
			if err != nil {
				return
			}
			go handleMockDockerdConn(conn)
		}
	}()

	// 2. Start Proxy
	cfg := DockerProxyConfig{
		ListenSocketPath: proxySocket,
		BackendTransport: TransportConfig{
			Mode:       "unix",
			SocketPath: mockBackendSocket,
		},
		GuestMountPrefix: "/mnt/mac",
	}

	proxy, err := NewDockerProxy(cfg)
	if err != nil {
		t.Fatalf("failed to create proxy: %v", err)
	}
	if err := proxy.Start(); err != nil {
		t.Fatalf("failed to start proxy: %v", err)
	}
	defer proxy.Stop()

	// Wait briefly for proxy socket
	time.Sleep(50 * time.Millisecond)

	// 3. Test HTTP Client over proxy socket
	httpClient := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return net.Dial("unix", proxySocket)
			},
		},
	}
	defer httpClient.CloseIdleConnections()

	// 3a. Ping test
	resp, err := httpClient.Get("http://docker/_ping")
	if err != nil {
		t.Fatalf("ping request failed: %v", err)
	}
	pingBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(pingBody) != "OK\n" {
		t.Fatalf("unexpected ping body: %s", string(pingBody))
	}

	// 3b. Create container with path translation
	createReq := `{"Image":"ubuntu","HostConfig":{"Binds":["/Users/adammaged/code:/app"]}}`
	createResp, err := httpClient.Post("http://docker/v1.45/containers/create", "application/json", strings.NewReader(createReq))
	if err != nil {
		t.Fatalf("create request failed: %v", err)
	}
	createBody, _ := io.ReadAll(createResp.Body)
	createResp.Body.Close()

	var createObj map[string]interface{}
	_ = json.Unmarshal(createBody, &createObj)
	if createObj["received_bind"].(string) != "/mnt/mac/Users/adammaged/code:/app" {
		t.Fatalf("backend did not receive translated bind! got: %v", createObj["received_bind"])
	}

	// 3c. Inspect container with reverse translation
	inspectResp, err := httpClient.Get("http://docker/v1.45/containers/123/json")
	if err != nil {
		t.Fatalf("inspect request failed: %v", err)
	}
	inspectBody, _ := io.ReadAll(inspectResp.Body)
	inspectResp.Body.Close()

	var inspectObj map[string]interface{}
	_ = json.Unmarshal(inspectBody, &inspectObj)
	mounts := inspectObj["Mounts"].([]interface{})
	m0 := mounts[0].(map[string]interface{})
	if m0["Source"].(string) != "/Users/adammaged/code" {
		t.Fatalf("client did not receive reverse translated source! got: %v", m0["Source"])
	}

	// 4. Test Stream Hijack (Interactive exec session)
	clientRawConn, err := net.Dial("unix", proxySocket)
	if err != nil {
		t.Fatalf("failed to dial proxy socket for hijack: %v", err)
	}
	defer clientRawConn.Close()

	hijackReq := "POST /v1.45/exec/abc/start HTTP/1.1\r\n" +
		"Host: docker\r\n" +
		"Upgrade: tcp\r\n" +
		"Connection: Upgrade\r\n" +
		"Content-Type: application/json\r\n\r\n"

	_, err = clientRawConn.Write([]byte(hijackReq))
	if err != nil {
		t.Fatalf("failed to write hijack req: %v", err)
	}

	clientReader := bufio.NewReader(clientRawConn)
	hijackResp, err := http.ReadResponse(clientReader, nil)
	if err != nil {
		t.Fatalf("failed to read hijack response: %v", err)
	}
	if hijackResp.StatusCode != 101 {
		t.Fatalf("expected status 101 UPGRADED, got: %d", hijackResp.StatusCode)
	}

	// Send bidirectional interactive terminal command
	testCommand := "echo 'OrbStack Free rocks!'\n"
	_, err = clientRawConn.Write([]byte(testCommand))
	if err != nil {
		t.Fatalf("failed to write interactive bytes: %v", err)
	}

	// Read echoed output
	line, err := clientReader.ReadString('\n')
	if err != nil {
		t.Fatalf("failed to read echoed output: %v", err)
	}
	if !strings.Contains(line, "ECHO: echo 'OrbStack Free rocks!'") {
		t.Fatalf("unexpected hijacked stream output: %s", line)
	}
}

// handleMockDockerdConn mocks Docker daemon behavior for testing
func handleMockDockerdConn(conn net.Conn) {
	defer conn.Close()
	buf := bufio.NewReader(conn)

	for {
		req, err := http.ReadRequest(buf)
		if err != nil {
			return
		}

		// Handle Stream Hijack
		if req.Header.Get("Upgrade") == "tcp" || strings.Contains(req.URL.Path, "/exec/") {
			resp := "HTTP/1.1 101 UPGRADED\r\n" +
				"Content-Type: application/vnd.docker.raw-stream\r\n" +
				"Connection: Upgrade\r\n" +
				"Upgrade: tcp\r\n\r\n"
			_, _ = conn.Write([]byte(resp))

			// Raw echo loop
			for {
				line, err := buf.ReadString('\n')
				if err != nil {
					return
				}
				echoResp := fmt.Sprintf("ECHO: %s", line)
				_, _ = conn.Write([]byte(echoResp))
			}
		}

		if req.URL.Path == "/_ping" {
			body := "OK\n"
			resp := fmt.Sprintf("HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\nContent-Length: %d\r\n\r\n%s", len(body), body)
			_, _ = conn.Write([]byte(resp))
			continue
		}

		if req.Method == "POST" && strings.Contains(req.URL.Path, "/containers/create") {
			bodyBytes, _ := io.ReadAll(req.Body)
			var root map[string]interface{}
			_ = json.Unmarshal(bodyBytes, &root)
			hc := root["HostConfig"].(map[string]interface{})
			binds := hc["Binds"].([]interface{})

			respMap := map[string]interface{}{
				"Id":            "container-uuid-12345",
				"received_bind": binds[0],
			}
			respBytes, _ := json.Marshal(respMap)
			resp := fmt.Sprintf("HTTP/1.1 201 Created\r\nContent-Type: application/json\r\nContent-Length: %d\r\n\r\n%s", len(respBytes), string(respBytes))
			_, _ = conn.Write([]byte(resp))
			continue
		}

		if req.Method == "GET" && strings.Contains(req.URL.Path, "/containers/") && strings.HasSuffix(req.URL.Path, "/json") {
			respMap := map[string]interface{}{
				"Id": "container-uuid-12345",
				"Mounts": []interface{}{
					map[string]interface{}{
						"Type":        "bind",
						"Source":      "/mnt/mac/Users/adammaged/code",
						"Destination": "/app",
					},
				},
				"HostConfig": map[string]interface{}{
					"Binds": []interface{}{
						"/mnt/mac/Users/adammaged/code:/app",
					},
				},
			}
			respBytes, _ := json.Marshal(respMap)
			resp := fmt.Sprintf("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: %d\r\n\r\n%s", len(respBytes), string(respBytes))
			_, _ = conn.Write([]byte(resp))
			continue
		}

		// Fallback
		resp := "HTTP/1.1 404 Not Found\r\nContent-Length: 0\r\n\r\n"
		_, _ = conn.Write([]byte(resp))
	}
}
