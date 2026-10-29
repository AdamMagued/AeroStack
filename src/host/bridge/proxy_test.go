package main

import (
	"bufio"
	"bytes"
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
)

func TestPathTranslator_Translation(t *testing.T) {
	pt := NewPathTranslator("/mnt/mac")

	// Host -> Guest
	h1 := "/Users/adammaged/code/app"
	g1 := pt.HostToGuest(h1)
	if g1 != "/mnt/mac/Users/adammaged/code/app" {
		t.Fatalf("Expected /mnt/mac/Users/adammaged/code/app, got %s", g1)
	}

	// Reverse Guest -> Host
	rh1 := pt.GuestToHost(g1)
	if rh1 != h1 {
		t.Fatalf("Reverse translation mismatch: %s != %s", rh1, h1)
	}

	// Special socket unchanged
	sock := "/var/run/docker.sock"
	if pt.HostToGuest(sock) != sock || pt.GuestToHost(sock) != sock {
		t.Fatalf("Special socket was altered")
	}

	// Custom mapping
	pt.AddMapping("/Volumes/FastDisk", "/mnt/fast")
	gCustom := pt.HostToGuest("/Volumes/FastDisk/data/db")
	if gCustom != "/mnt/fast/data/db" {
		t.Fatalf("Custom mapping failed: %s", gCustom)
	}
	rCustom := pt.GuestToHost(gCustom)
	if rCustom != "/Volumes/FastDisk/data/db" {
		t.Fatalf("Reverse custom mapping failed: %s", rCustom)
	}
}

func TestPathTranslator_ContainerCreateRewrite(t *testing.T) {
	pt := NewPathTranslator("/mnt/mac")

	reqJSON := `{
		"Image": "node:18",
		"HostConfig": {
			"Binds": [
				"/Users/adammaged/frontend:/app:ro",
				"/var/run/docker.sock:/var/run/docker.sock"
			]
		}
	}`

	rewritten, err := pt.TranslateContainerCreateRequestBody([]byte(reqJSON))
	if err != nil {
		t.Fatalf("Failed to rewrite body: %v", err)
	}

	var root map[string]interface{}
	if err := json.Unmarshal(rewritten, &root); err != nil {
		t.Fatalf("Failed to unmarshal rewritten: %v", err)
	}

	hostCfg := root["HostConfig"].(map[string]interface{})
	binds := hostCfg["Binds"].([]interface{})
	if len(binds) != 2 {
		t.Fatalf("Expected 2 binds, got %d", len(binds))
	}

	if binds[0].(string) != "/mnt/mac/Users/adammaged/frontend:/app:ro" {
		t.Fatalf("Unexpected rewritten bind: %s", binds[0])
	}
	if binds[1].(string) != "/var/run/docker.sock:/var/run/docker.sock" {
		t.Fatalf("Docker socket was rewritten: %s", binds[1])
	}
}

func TestPathTranslator_ContainerInspectRewrite(t *testing.T) {
	pt := NewPathTranslator("/mnt/mac")

	inspectJSON := `{
		"Id": "container123",
		"Mounts": [
			{
				"Type": "bind",
				"Source": "/mnt/mac/Users/adammaged/app",
				"Destination": "/app"
			}
		],
		"HostConfig": {
			"Binds": [
				"/mnt/mac/Users/adammaged/app:/app"
			]
		}
	}`

	rewritten, err := pt.TranslateContainerInspectResponseBody([]byte(inspectJSON))
	if err != nil {
		t.Fatalf("Failed to rewrite inspect response: %v", err)
	}

	var root map[string]interface{}
	_ = json.Unmarshal(rewritten, &root)
	mounts := root["Mounts"].([]interface{})
	m0 := mounts[0].(map[string]interface{})
	if m0["Source"].(string) != "/Users/adammaged/app" {
		t.Fatalf("Expected stripped /Users/adammaged/app, got: %s", m0["Source"])
	}
}

func TestHijackDetection(t *testing.T) {
	headers := map[string][]string{
		"Upgrade":    {"tcp"},
		"Connection": {"Upgrade"},
	}
	if !IsHijackRequest("POST", "/v1.45/containers/c1/attach?stdin=1", headers) {
		t.Fatalf("Expected hijack request to be detected")
	}

	respHeaders := map[string][]string{
		"Content-Type": {"application/vnd.docker.raw-stream"},
	}
	if !IsHijackResponse(200, respHeaders) {
		t.Fatalf("Expected raw-stream response to be detected")
	}
	if !IsHijackResponse(101, nil) {
		t.Fatalf("Expected 101 Switching Protocols to be detected")
	}
}

func TestDockerProxy_EndToEnd(t *testing.T) {
	tmpDir, err := os.MkdirTemp("/tmp", "dk_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)
	backendSock := filepath.Join(tmpDir, "b.sock")
	clientSock := filepath.Join(tmpDir, "c.sock")

	var lastReceivedBody []byte

	// Mock Docker Engine HTTP backend
	backendListener, err := net.Listen("unix", backendSock)
	if err != nil {
		t.Fatalf("Failed to listen backend: %v", err)
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
				req, err := http.ReadRequest(bufioReader(c))
				if err != nil {
					return
				}
				if req.Body != nil {
					lastReceivedBody, _ = io.ReadAll(req.Body)
				}

				resp := "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: 15\r\n\r\n{\"status\":\"ok\"}"
				_, _ = c.Write([]byte(resp))
			}(conn)
		}
	}()

	// Start Docker Proxy
	proxy, err := NewDockerProxy(DockerProxyConfig{
		ListenSocketPaths: []string{clientSock},
		GuestMountPrefix:  "/mnt/mac",
		BackendTransport: TransportConfig{
			Mode:       "unix",
			SocketPath: backendSock,
		},
	})
	if err != nil {
		t.Fatalf("Failed to create proxy: %v", err)
	}
	if err := proxy.Start(); err != nil {
		t.Fatalf("Failed to start proxy: %v", err)
	}
	defer proxy.Stop()

	// Connect client to proxy socket
	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return net.Dial("unix", clientSock)
			},
		},
	}

	payload := `{"Image":"alpine","HostConfig":{"Binds":["/Users/adammaged/dev:/app"]}}`
	httpReq, _ := http.NewRequest("POST", "http://localhost/v1.45/containers/create", strings.NewReader(payload))
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(httpReq)
	if err != nil {
		t.Fatalf("Proxy request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d", resp.StatusCode)
	}

	// Verify that the backend received the rewritten path!
	if !bytes.Contains(lastReceivedBody, []byte("/mnt/mac/Users/adammaged/dev:/app")) {
		t.Fatalf("Backend did not receive translated path! Received: %s", string(lastReceivedBody))
	}
}

func TestPathTranslator_WorkspaceAndSSD(t *testing.T) {
	pt := NewPathTranslator("/workspace")

	hostPath := "/Volumes/AdamSSD/antigravity_workspace/orbstack_free"
	guestPath := pt.HostToGuest(hostPath)
	expectedGuest := "/workspace/orbstack_free"
	if guestPath != expectedGuest {
		t.Fatalf("Expected %s, got %s", expectedGuest, guestPath)
	}

	reversed := pt.GuestToHost(guestPath)
	if reversed != hostPath {
		t.Fatalf("Expected %s, got %s", hostPath, reversed)
	}

	createReq := []byte(`{
		"Image": "alpine",
		"HostConfig": {
			"Binds": ["/Volumes/AdamSSD/antigravity_workspace/orbstack_free:/test:rw"]
		}
	}`)
	rewritten, err := pt.TranslateContainerCreateRequestBody(createReq)
	if err != nil {
		t.Fatalf("TranslateContainerCreateRequestBody failed: %v", err)
	}
	if !bytes.Contains(rewritten, []byte(`"/workspace/orbstack_free:/test:rw"`)) {
		t.Fatalf("Expected rewritten bind mount /workspace/orbstack_free:/test:rw, got: %s", string(rewritten))
	}
}

func TestPathTranslator_MountsArrayTranslation(t *testing.T) {
	pt := NewPathTranslator("/workspace")

	createReq := []byte(`{
		"Image": "alpine",
		"Mounts": [
			{
				"Type": "bind",
				"Source": "/Volumes/AdamSSD/antigravity_workspace/test_app",
				"Target": "/app"
			},
			{
				"Type": "bind",
				"Source": "/Users/adammaged/code",
				"Target": "/code"
			}
		]
	}`)
	rewritten, err := pt.TranslateContainerCreateRequestBody(createReq)
	if err != nil {
		t.Fatalf("TranslateContainerCreateRequestBody failed: %v", err)
	}

	var parsed struct {
		Mounts []struct {
			Source string `json:"Source"`
			Target string `json:"Target"`
		} `json:"Mounts"`
	}
	if err := json.Unmarshal(rewritten, &parsed); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	if len(parsed.Mounts) != 2 {
		t.Fatalf("Expected 2 mounts, got %d", len(parsed.Mounts))
	}
	if parsed.Mounts[0].Source != "/workspace/test_app" {
		t.Fatalf("Expected /workspace/test_app, got %s", parsed.Mounts[0].Source)
	}
	if parsed.Mounts[1].Source != "/mnt/mac/Users/adammaged/code" {
		t.Fatalf("Expected /mnt/mac/Users/adammaged/code, got %s", parsed.Mounts[1].Source)
	}
}

func TestDockerProxy_ContainersListNotInspect(t *testing.T) {
	tmpDir, err := os.MkdirTemp("/tmp", "dk_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)
	backendSock := filepath.Join(tmpDir, "b.sock")
	clientSock := filepath.Join(tmpDir, "c.sock")

	bListener, err := net.Listen("unix", backendSock)
	if err != nil {
		t.Fatalf("Failed to bind mock backend: %v", err)
	}
	defer bListener.Close()

	listPayload := `[{"Id":"c1","Names":["/web"],"Image":"nginx"}]`
	go func() {
		conn, err := bListener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		req, _ := http.ReadRequest(bufio.NewReader(conn))
		if req != nil {
			resp := fmt.Sprintf("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: %d\r\n\r\n%s", len(listPayload), listPayload)
			_, _ = conn.Write([]byte(resp))
		}
	}()

	cfg := DockerProxyConfig{
		ListenSocketPaths: []string{clientSock},
		BackendTransport: TransportConfig{
			Mode:       "unix",
			SocketPath: backendSock,
		},
		GuestMountPrefix: "/mnt/mac",
	}

	proxy, err := NewDockerProxy(cfg)
	if err != nil {
		t.Fatalf("Failed to init proxy: %v", err)
	}
	if err := proxy.Start(); err != nil {
		t.Fatalf("Failed to start proxy: %v", err)
	}
	defer proxy.Stop()

	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, proto, addr string) (net.Conn, error) {
				return net.Dial("unix", clientSock)
			},
		},
	}

	httpReq, _ := http.NewRequest("GET", "http://localhost/v1.45/containers/json", nil)
	resp, err := client.Do(httpReq)
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	defer resp.Body.Close()

	bodyBytes, _ := io.ReadAll(resp.Body)
	if string(bodyBytes) != listPayload {
		t.Fatalf("Expected raw array response, got: %s", string(bodyBytes))
	}
}

func bufioReader(c net.Conn) *bufio.Reader {
	return bufio.NewReader(c)
}

func TestHijacker_HalfClose(t *testing.T) {
	cClient, cProxy, err := socketPair()
	if err != nil {
		t.Fatalf("socketPair failed: %v", err)
	}
	defer cClient.Close()
	defer cProxy.Close()

	bProxy, bBackend, err := socketPair()
	if err != nil {
		t.Fatalf("socketPair failed: %v", err)
	}
	defer bProxy.Close()
	defer bBackend.Close()

	backendDone := make(chan struct{})
	go func() {
		defer close(backendDone)
		reqData, err := io.ReadAll(bBackend)
		if err != nil {
			return
		}
		_, _ = bBackend.Write(append([]byte("HIJACK_REPLY:"), reqData...))
		if cw, ok := bBackend.(interface{ CloseWrite() error }); ok {
			_ = cw.CloseWrite()
		}
	}()

	statsDone := make(chan *StreamStats, 1)
	go func() {
		stats, hErr := SpliceHijackedStream(cProxy, nil, bProxy, nil)
		if hErr != nil {
			t.Errorf("SpliceHijackedStream error: %v", hErr)
		}
		statsDone <- stats
	}()

	testMsg := "echo_half_close_test"
	if _, err := cClient.Write([]byte(testMsg)); err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	if cw, ok := cClient.(interface{ CloseWrite() error }); ok {
		if err := cw.CloseWrite(); err != nil {
			t.Fatalf("CloseWrite failed: %v", err)
		}
	} else {
		t.Fatalf("cClient does not implement CloseWrite")
	}

	resp, err := io.ReadAll(cClient)
	if err != nil {
		t.Fatalf("Failed to read reply: %v", err)
	}

	expected := "HIJACK_REPLY:" + testMsg
	if string(resp) != expected {
		t.Fatalf("Expected %q, got %q", expected, string(resp))
	}

	<-backendDone
	stats := <-statsDone
	if stats.BytesClientToBackend != int64(len(testMsg)) {
		t.Fatalf("BytesClientToBackend mismatch: %d != %d", stats.BytesClientToBackend, len(testMsg))
	}
}

func socketPair() (net.Conn, net.Conn, error) {
	tmpDir, err := os.MkdirTemp("/tmp", "sp_*")
	if err != nil {
		return nil, nil, err
	}
	defer os.RemoveAll(tmpDir)
	sockPath := filepath.Join(tmpDir, "p.sock")

	l, err := net.Listen("unix", sockPath)
	if err != nil {
		return nil, nil, err
	}
	defer l.Close()

	type connResult struct {
		c   net.Conn
		err error
	}
	resCh := make(chan connResult, 1)
	go func() {
		c, err := l.Accept()
		resCh <- connResult{c, err}
	}()

	c1, err := net.Dial("unix", sockPath)
	if err != nil {
		return nil, nil, err
	}
	res := <-resCh
	if res.err != nil {
		c1.Close()
		return nil, nil, res.err
	}
	return c1, res.c, nil
}

