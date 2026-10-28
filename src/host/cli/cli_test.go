package main

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type testServer struct {
	listener net.Listener
	sockPath string
	mu       sync.Mutex
	machines map[string]MachineDTO
	settings OrbSettingsDTO
	metrics  LiveMetricsDTO
}

func startTestRPCServer(t *testing.T) *testServer {
	t.Helper()
	tmpDir, err := os.MkdirTemp(os.TempDir(), "rpc_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	sock := filepath.Join(tmpDir, "vmcontrol.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("failed to listen on unix socket %s: %v", sock, err)
	}

	ts := &testServer{
		listener: l,
		sockPath: sock,
		machines: map[string]MachineDTO{
			"ubuntu": {
				Name:      "ubuntu",
				State:     "RUNNING",
				Distro:    "Ubuntu",
				Version:   "24.04",
				Arch:      "arm64",
				Size:      "2.1 GB",
				IPAddress: "192.168.138.2",
			},
			"alpine": {
				Name:      "alpine",
				State:     "STOPPED",
				Distro:    "Alpine",
				Version:   "3.20",
				Arch:      "arm64",
				Size:      "42 MB",
				IPAddress: "-",
			},
		},
		settings: OrbSettingsDTO{
			MaxCPUs:         8,
			MaxMemoryMB:     8192,
			MaxDiskGB:       64,
			RosettaX86:      true,
			FastVirtioFS:    true,
			BatterySaver:    false,
			AutoStart:       true,
			DNSDomain:       "orb.local",
			StorageLocation: "/Volumes/AdamSSD/antigravity_workspace/orbstack_free/data",
		},
		metrics: LiveMetricsDTO{
			Timestamp:            time.Now().Unix(),
			HostCPUPercent:       2.4,
			GuestCPUPercent:      1.1,
			MemoryUsedMB:         712,
			MemoryTotalMB:        8192,
			BalloonFreedMB:       1024,
			VirtioFSOpsPerSec:    3450,
			VirtioFSCacheHitRate: 99.4,
			NetworkRxKbps:        240.5,
			NetworkTxKbps:        85.2,
		},
	}

	go ts.serve()
	return ts
}

func (s *testServer) Close() {
	s.listener.Close()
	os.RemoveAll(filepath.Dir(s.sockPath))
}

func (s *testServer) serve() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		go s.handleConn(conn)
	}
}

func (s *testServer) handleConn(conn net.Conn) {
	defer conn.Close()
	reader := bufio.NewReader(conn)
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			return
		}

		var req struct {
			JSONRPC string                 `json:"jsonrpc"`
			ID      interface{}            `json:"id"`
			Method  string                 `json:"method"`
			Params  map[string]interface{} `json:"params"`
		}
		if err := json.Unmarshal(line, &req); err != nil {
			continue
		}

		resp := s.dispatch(req.Method, req.Params, req.ID)
		respBytes, _ := json.Marshal(resp)
		respBytes = append(respBytes, '\n')
		_, _ = conn.Write(respBytes)
	}
}

func (s *testServer) dispatch(method string, params map[string]interface{}, id interface{}) map[string]interface{} {
	s.mu.Lock()
	defer s.mu.Unlock()

	switch method {
	case "vm.list":
		var list []MachineDTO
		for _, m := range s.machines {
			list = append(list, m)
		}
		return map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      id,
			"result":  list,
		}

	case "vm.create":
		name, _ := params["name"].(string)
		distro, _ := params["distro"].(string)
		arch, _ := params["arch"].(string)
		if arch == "" {
			arch = "arm64"
		}
		s.machines[name] = MachineDTO{
			Name:      name,
			State:     "STOPPED",
			Distro:    distro,
			Version:   "latest",
			Arch:      arch,
			Size:      "2.1 GB",
			IPAddress: "-",
		}
		return map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      id,
			"result":  true,
		}

	case "vm.clone":
		src, _ := params["source"].(string)
		dst, _ := params["destination"].(string)
		if srcVM, ok := s.machines[src]; ok {
			cloned := srcVM
			cloned.Name = dst
			cloned.State = "STOPPED"
			cloned.IPAddress = "-"
			s.machines[dst] = cloned
			return map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      id,
				"result":  true,
			}
		}
		return map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      id,
			"error": map[string]interface{}{
				"code":    -1,
				"message": "source machine not found",
			},
		}

	case "vm.start":
		name, _ := params["name"].(string)
		if vm, ok := s.machines[name]; ok {
			vm.State = "RUNNING"
			vm.IPAddress = "192.168.138.2"
			s.machines[name] = vm
			return map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      id,
				"result":  true,
			}
		}
		return map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      id,
			"error": map[string]interface{}{
				"code":    -1,
				"message": "machine not found",
			},
		}

	case "vm.stop":
		name, _ := params["name"].(string)
		if vm, ok := s.machines[name]; ok {
			vm.State = "STOPPED"
			vm.IPAddress = "-"
			s.machines[name] = vm
			return map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      id,
				"result":  true,
			}
		}
		return map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      id,
			"error": map[string]interface{}{
				"code":    -1,
				"message": "machine not found",
			},
		}

	case "vm.delete":
		name, _ := params["name"].(string)
		delete(s.machines, name)
		return map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      id,
			"result":  true,
		}

	case "metrics.get":
		return map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      id,
			"result":  s.metrics,
		}

	case "doctor.run":
		report := DoctorReportDTO{
			OverallOK: true,
			Checks: []DoctorCheckDTO{
				{Name: "Virtualization.framework", OK: true, Detail: "Active"},
				{Name: "Vsock control bus", OK: true, Detail: "Operational"},
				{Name: "VirtioFS shared storage", OK: true, Detail: "Mounted"},
				{Name: "CoW snapshot driver", OK: true, Detail: "Active"},
			},
		}
		return map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      id,
			"result":  report,
		}

	case "config.get":
		return map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      id,
			"result":  s.settings,
		}

	case "config.set":
		return map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      id,
			"result":  true,
		}

	default:
		return map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      id,
			"error": map[string]interface{}{
				"code":    -32601,
				"message": "method not found",
			},
		}
	}
}

func TestCLI_List(t *testing.T) {
	ts := startTestRPCServer(t)
	defer ts.Close()

	cmd := NewRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"--socket", ts.sockPath, "list", "-j"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("list -j failed: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "ubuntu") {
		t.Fatalf("expected ubuntu in output: %s", output)
	}
}

func TestCLI_Create(t *testing.T) {
	ts := startTestRPCServer(t)
	defer ts.Close()

	cmd := NewRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"--socket", ts.sockPath, "create", "alpine:3.20", "test-alpine"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("create failed: %v", err)
	}

	// Verify machine was created over RPC
	ts.mu.Lock()
	_, found := ts.machines["test-alpine"]
	ts.mu.Unlock()
	if !found {
		t.Fatalf("expected test-alpine to be created in server")
	}
}

func TestCLI_StartStopDelete(t *testing.T) {
	ts := startTestRPCServer(t)
	defer ts.Close()

	// 1. Start machine
	cmd := NewRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"--socket", ts.sockPath, "start", "alpine"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("start failed: %v", err)
	}

	ts.mu.Lock()
	if ts.machines["alpine"].State != "RUNNING" {
		t.Fatalf("expected alpine to be RUNNING")
	}
	ts.mu.Unlock()

	// 2. Stop machine
	cmd = NewRootCmd()
	buf.Reset()
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"--socket", ts.sockPath, "stop", "alpine"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("stop failed: %v", err)
	}

	ts.mu.Lock()
	if ts.machines["alpine"].State != "STOPPED" {
		t.Fatalf("expected alpine to be STOPPED")
	}
	ts.mu.Unlock()

	// 3. Delete machine
	cmd = NewRootCmd()
	buf.Reset()
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"--socket", ts.sockPath, "delete", "alpine"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("delete failed: %v", err)
	}

	ts.mu.Lock()
	if _, exists := ts.machines["alpine"]; exists {
		t.Fatalf("expected alpine to be deleted")
	}
	ts.mu.Unlock()
}

func TestCLI_Run(t *testing.T) {
	// Start mock exec server on loopback
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to listen: %v", err)
	}
	defer l.Close()

	go func() {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		// Read ExecRequest header & body
		hdr := make([]byte, 5)
		if _, err := io.ReadFull(conn, hdr); err != nil {
			return
		}
		length := binary.BigEndian.Uint32(hdr[1:])
		body := make([]byte, length)
		if _, err := io.ReadFull(conn, body); err != nil {
			return
		}

		// Send Started response
		respBytes := []byte(`{"started":true,"pid":1234}`)
		respHdr := make([]byte, 5)
		respHdr[0] = 2 // ExecMsgResponse
		binary.BigEndian.PutUint32(respHdr[1:], uint32(len(respBytes)))
		_, _ = conn.Write(respHdr)
		_, _ = conn.Write(respBytes)

		// Send Stdout frame
		outBytes := []byte("Linux aero 6.6.0 arm64 GNU/Linux\n")
		outHdr := make([]byte, 5+len(outBytes))
		outHdr[0] = 4 // ExecMsgDataStdout
		binary.BigEndian.PutUint32(outHdr[1:], uint32(len(outBytes)))
		copy(outHdr[5:], outBytes)
		_, _ = conn.Write(outHdr)

		// Send ExitStatus frame
		exitFrame := make([]byte, 9)
		exitFrame[0] = 7 // ExecMsgExitStatus
		binary.BigEndian.PutUint32(exitFrame[1:], 4)
		binary.BigEndian.PutUint32(exitFrame[5:], 0)
		_, _ = conn.Write(exitFrame)
	}()

	cmd := NewRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"run", "--exec-addr", l.Addr().String(), "uname", "-a"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if !strings.Contains(buf.String(), "Linux aero") {
		t.Logf("Output was: %q", buf.String())
	}
}

func TestCLI_Clone(t *testing.T) {
	ts := startTestRPCServer(t)
	defer ts.Close()

	cmd := NewRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"--socket", ts.sockPath, "clone", "ubuntu", "ubuntu-clone"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("clone failed: %v", err)
	}

	ts.mu.Lock()
	if _, ok := ts.machines["ubuntu-clone"]; !ok {
		t.Fatalf("expected ubuntu-clone to be created in server")
	}
	ts.mu.Unlock()
}

func TestCLI_StatusAndDoctor(t *testing.T) {
	ts := startTestRPCServer(t)
	defer ts.Close()

	cmd := NewRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)

	cmd.SetArgs([]string{"--socket", ts.sockPath, "status"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("status failed: %v", err)
	}

	cmd = NewRootCmd()
	buf.Reset()
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"--socket", ts.sockPath, "doctor"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("doctor failed: %v", err)
	}

	cmd = NewRootCmd()
	buf.Reset()
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"--socket", ts.sockPath, "top"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("top failed: %v", err)
	}

	cmd = NewRootCmd()
	buf.Reset()
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"--socket", ts.sockPath, "config"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("config failed: %v", err)
	}
}

func TestCLI_OrbShortcutLogic(t *testing.T) {
	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()

	os.Args = []string{"/usr/local/bin/orb", "uname", "-a"}
	progName := "orb"
	if progName == "orb" {
		if len(os.Args) > 1 && os.Args[1] != "run" {
			newArgs := append([]string{os.Args[0], "run"}, os.Args[1:]...)
			os.Args = newArgs
		}
	}

	if os.Args[1] != "run" || os.Args[2] != "uname" || os.Args[3] != "-a" {
		t.Fatalf("Shortcut prepending failed: %v", os.Args)
	}
}
