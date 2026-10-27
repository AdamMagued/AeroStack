package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// RPCServer handles UI communication over Unix socket
type RPCServer struct {
	socketPath string
	listener   net.Listener
	mu         sync.RWMutex
	settings   OrbSettings
	vms        map[string]*VMSummary
	stopped    bool
}

// NewRPCServer initializes the UI daemon server
func NewRPCServer(socketPath string) *RPCServer {
	srv := &RPCServer{
		socketPath: socketPath,
		vms:        make(map[string]*VMSummary),
		settings: OrbSettings{
			MaxCPUs:         10,
			MaxMemoryMB:     8192,
			MaxDiskGB:       64,
			RosettaX86:      true,
			FastVirtioFS:    true,
			BatterySaver:    false,
			AutoStart:       true,
			DNSDomain:       "orb.local",
			StorageLocation: "/Volumes/AdamSSD/orbstack_data",
		},
	}

	// Seed sample machines
	srv.vms["ubuntu"] = &VMSummary{
		Name:      "ubuntu",
		Distro:    "Ubuntu",
		Version:   "24.04",
		Arch:      "arm64",
		State:     "RUNNING",
		CPUs:      4,
		MemoryMB:  4096,
		IPAddress: "192.168.215.2",
		Size:      "1.8 GB",
		UptimeSec: 14200,
	}
	srv.vms["alpine"] = &VMSummary{
		Name:      "alpine",
		Distro:    "Alpine",
		Version:   "3.20",
		Arch:      "arm64",
		State:     "STOPPED",
		CPUs:      1,
		MemoryMB:  512,
		IPAddress: "-",
		Size:      "42 MB",
		UptimeSec: 0,
	}

	return srv
}

// Start begins listening on Unix socket
func (s *RPCServer) Start() error {
	dir := filepath.Dir(s.socketPath)
	_ = os.MkdirAll(dir, 0755)
	_ = os.Remove(s.socketPath)

	l, err := net.Listen("unix", s.socketPath)
	if err != nil {
		return fmt.Errorf("failed to listen on rpc socket %s: %w", s.socketPath, err)
	}
	_ = os.Chmod(s.socketPath, 0666)

	s.listener = l
	go s.acceptLoop()
	return nil
}

// Stop terminates the server
func (s *RPCServer) Stop() error {
	s.mu.Lock()
	s.stopped = true
	s.mu.Unlock()

	if s.listener != nil {
		_ = s.listener.Close()
	}
	_ = os.Remove(s.socketPath)
	return nil
}

func (s *RPCServer) acceptLoop() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			s.mu.RLock()
			stopped := s.stopped
			s.mu.RUnlock()
			if stopped {
				return
			}
			continue
		}
		go s.handleClient(conn)
	}
}

func (s *RPCServer) handleClient(conn net.Conn) {
	defer conn.Close()
	reader := bufio.NewReader(conn)

	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			if err != io.EOF {
				log.Printf("[rpc-server] read error: %v", err)
			}
			return
		}

		var req RPCRequest
		if err := json.Unmarshal(line, &req); err != nil {
			s.sendError(conn, nil, ErrParseError, "Parse error")
			continue
		}

		resp := s.dispatch(&req)
		respBytes, _ := json.Marshal(resp)
		respBytes = append(respBytes, '\n')
		if _, werr := conn.Write(respBytes); werr != nil {
			return
		}
	}
}

func (s *RPCServer) dispatch(req *RPCRequest) RPCResponse {
	resp := RPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
	}

	switch req.Method {
	case "vm.list":
		s.mu.RLock()
		list := make([]VMSummary, 0, len(s.vms))
		for _, vm := range s.vms {
			list = append(list, *vm)
		}
		s.mu.RUnlock()
		resp.Result = list

	case "vm.start":
		var params struct{ Name string `json:"name"` }
		_ = json.Unmarshal(req.Params, &params)
		s.mu.Lock()
		if vm, ok := s.vms[params.Name]; ok {
			vm.State = "RUNNING"
			vm.IPAddress = "192.168.215.4"
			resp.Result = true
		} else {
			resp.Error = &RPCError{Code: -1, Message: "Machine not found"}
		}
		s.mu.Unlock()

	case "vm.stop":
		var params struct{ Name string `json:"name"` }
		_ = json.Unmarshal(req.Params, &params)
		s.mu.Lock()
		if vm, ok := s.vms[params.Name]; ok {
			vm.State = "STOPPED"
			resp.Result = true
		} else {
			resp.Error = &RPCError{Code: -1, Message: "Machine not found"}
		}
		s.mu.Unlock()

	case "container.list":
		resp.Result = []ContainerSummary{
			{
				ID:      "cbe6e9fc1b91",
				Names:   []string{"/fervent_galileo"},
				Image:   "pr-sandbox:latest",
				Command: "/bin/bash",
				State:   "running",
				Status:  "Up 45 minutes",
				Ports:   []string{"8080:80/tcp"},
				Mounts: map[string]string{
					"/workspace": "/Volumes/AdamSSD/antigravity_workspace",
				},
			},
		}

	case "metrics.get":
		resp.Result = LiveMetrics{
			Timestamp:       time.Now().Unix(),
			HostCPUPercent:  2.4,
			GuestCPUPercent: 1.1,
			MemoryUsedMB:    740,
			MemoryTotalMB:   8192,
			BalloonFreedMB:  1420,
			VirtioFSOpsPerS: 3200,
			VirtioFSHitRate: 98.7,
			NetworkRxKBps:   124.5,
			NetworkTxKBps:   48.2,
		}

	case "config.get":
		s.mu.RLock()
		resp.Result = s.settings
		s.mu.RUnlock()

	case "config.set":
		var newSettings OrbSettings
		if err := json.Unmarshal(req.Params, &newSettings); err == nil {
			s.mu.Lock()
			s.settings = newSettings
			s.mu.Unlock()
			resp.Result = true
		} else {
			resp.Error = &RPCError{Code: ErrInvalidParams, Message: err.Error()}
		}

	default:
		resp.Error = &RPCError{Code: ErrMethodNotFound, Message: fmt.Sprintf("Method %s not found", req.Method)}
	}

	return resp
}

func (s *RPCServer) sendError(conn net.Conn, id interface{}, code int, message string) {
	resp := RPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &RPCError{Code: code, Message: message},
	}
	data, _ := json.Marshal(resp)
	data = append(data, '\n')
	_, _ = conn.Write(data)
}
