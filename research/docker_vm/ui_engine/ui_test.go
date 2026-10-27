package main

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestJSONRPCServerLifecycle(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "ui-rpc-test-*")
	if err != nil {
		t.Fatalf("temp dir error: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	sockPath := filepath.Join(tmpDir, "vmcontrol.sock")
	srv := NewRPCServer(sockPath)
	if err := srv.Start(); err != nil {
		t.Fatalf("failed to start rpc server: %v", err)
	}
	defer srv.Stop()

	// Wait briefly for socket
	time.Sleep(30 * time.Millisecond)

	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("failed to dial rpc socket: %v", err)
	}
	defer conn.Close()

	reader := bufio.NewReader(conn)

	// 1. Test vm.list
	req1 := `{"jsonrpc":"2.0","id":1,"method":"vm.list"}` + "\n"
	_, _ = conn.Write([]byte(req1))

	respLine1, err := reader.ReadBytes('\n')
	if err != nil {
		t.Fatalf("failed to read resp1: %v", err)
	}
	var resp1 RPCResponse
	_ = json.Unmarshal(respLine1, &resp1)
	if resp1.Error != nil {
		t.Fatalf("vm.list returned error: %v", resp1.Error)
	}

	// 2. Test metrics.get
	req2 := `{"jsonrpc":"2.0","id":2,"method":"metrics.get"}` + "\n"
	_, _ = conn.Write([]byte(req2))

	respLine2, err := reader.ReadBytes('\n')
	if err != nil {
		t.Fatalf("failed to read resp2: %v", err)
	}
	var resp2 RPCResponse
	_ = json.Unmarshal(respLine2, &resp2)
	if resp2.Error != nil {
		t.Fatalf("metrics.get returned error: %v", resp2.Error)
	}

	// 3. Test config.get
	req3 := `{"jsonrpc":"2.0","id":3,"method":"config.get"}` + "\n"
	_, _ = conn.Write([]byte(req3))

	respLine3, err := reader.ReadBytes('\n')
	if err != nil {
		t.Fatalf("failed to read resp3: %v", err)
	}
	var resp3 RPCResponse
	_ = json.Unmarshal(respLine3, &resp3)
	if resp3.Error != nil {
		t.Fatalf("config.get returned error: %v", resp3.Error)
	}

	// 4. Test invalid method
	req4 := `{"jsonrpc":"2.0","id":4,"method":"unknown.method"}` + "\n"
	_, _ = conn.Write([]byte(req4))

	respLine4, err := reader.ReadBytes('\n')
	if err != nil {
		t.Fatalf("failed to read resp4: %v", err)
	}
	var resp4 RPCResponse
	_ = json.Unmarshal(respLine4, &resp4)
	if resp4.Error == nil || resp4.Error.Code != ErrMethodNotFound {
		t.Fatalf("expected ErrMethodNotFound, got: %v", resp4.Error)
	}
}
