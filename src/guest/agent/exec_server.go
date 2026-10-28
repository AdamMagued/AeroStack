package main

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
)

// Direct Exec Protocol message types
type ExecMsgType uint8

const (
	ExecMsgRequest      ExecMsgType = 1
	ExecMsgResponse     ExecMsgType = 2
	ExecMsgDataStdin    ExecMsgType = 3
	ExecMsgDataStdout   ExecMsgType = 4
	ExecMsgDataStderr   ExecMsgType = 5
	ExecMsgWindowResize ExecMsgType = 6
	ExecMsgExitStatus   ExecMsgType = 7
)

// ExecRequest payload sent from host CLI to guest agent
type ExecRequest struct {
	Machine string            `json:"machine"`
	Command []string          `json:"command"`
	User    string            `json:"user"`
	WorkDir string            `json:"work_dir"`
	Env     map[string]string `json:"env"`
	Tty     bool              `json:"tty"`
	Rows    uint16            `json:"rows"`
	Cols    uint16            `json:"cols"`
}

// ExecResponse payload sent from guest agent to host CLI
type ExecResponse struct {
	PID     int    `json:"pid"`
	Started bool   `json:"started"`
	Error   string `json:"error,omitempty"`
}

// ExitStatusResponse reports process termination
type ExitStatusResponse struct {
	ExitCode int `json:"exit_code"`
}

// WindowResizePayload informs guest agent of terminal dimensions
type WindowResizePayload struct {
	Rows uint16 `json:"rows"`
	Cols uint16 `json:"cols"`
}

// ExecServerConfig holds configuration for the binary execution server
type ExecServerConfig struct {
	ListenPort uint32
}

// ExecServer runs inside Linux VM guest to provide instant sub-5ms command execution
type ExecServer struct {
	config      ExecServerConfig
	listener    net.Listener
	stopCh      chan struct{}
	wg          sync.WaitGroup
	running     int32
	activeMu    sync.Mutex
	activeConns map[net.Conn]struct{}
	execCount   uint64
}

// NewExecServer constructs a new execution server
func NewExecServer(cfg ExecServerConfig) *ExecServer {
	if cfg.ListenPort == 0 {
		cfg.ListenPort = 8022
	}
	return &ExecServer{
		config:      cfg,
		stopCh:      make(chan struct{}),
		activeConns: make(map[net.Conn]struct{}),
	}
}

// Start begins listening on vsock port 8022
func (s *ExecServer) Start() error {
	if !atomic.CompareAndSwapInt32(&s.running, 0, 1) {
		return fmt.Errorf("exec server already running")
	}

	l, err := ListenVsock(s.config.ListenPort)
	if err != nil {
		atomic.StoreInt32(&s.running, 0)
		return fmt.Errorf("failed to listen on vsock port %d: %w", s.config.ListenPort, err)
	}
	s.listener = l

	s.wg.Add(1)
	go s.acceptLoop()

	log.Printf("[orb-agent:exec] Direct PTY execution server listening on vsock port %d", s.config.ListenPort)
	return nil
}

// Stop cleanly terminates the execution server
func (s *ExecServer) Stop() {
	if atomic.CompareAndSwapInt32(&s.running, 1, 0) {
		close(s.stopCh)
		if s.listener != nil {
			_ = s.listener.Close()
		}

		s.activeMu.Lock()
		for c := range s.activeConns {
			_ = c.Close()
		}
		s.activeMu.Unlock()

		s.wg.Wait()
		log.Printf("[orb-agent:exec] Execution server stopped (Total commands executed: %d)",
			atomic.LoadUint64(&s.execCount))
	}
}

func (s *ExecServer) acceptLoop() {
	defer s.wg.Done()
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			if atomic.LoadInt32(&s.running) == 0 {
				return
			}
			continue
		}

		s.activeMu.Lock()
		s.activeConns[conn] = struct{}{}
		s.activeMu.Unlock()

		s.wg.Add(1)
		go s.handleConn(conn)
	}
}

func (s *ExecServer) handleConn(conn net.Conn) {
	defer s.wg.Done()
	defer func() {
		_ = conn.Close()
		s.activeMu.Lock()
		delete(s.activeConns, conn)
		s.activeMu.Unlock()
	}()

	// 1. Read ExecRequest frame header
	header := make([]byte, 5)
	if _, err := io.ReadFull(conn, header); err != nil {
		return
	}
	if ExecMsgType(header[0]) != ExecMsgRequest {
		return
	}

	length := binary.BigEndian.Uint32(header[1:])
	body := make([]byte, length)
	if _, err := io.ReadFull(conn, body); err != nil {
		return
	}

	var req ExecRequest
	if err := json.Unmarshal(body, &req); err != nil {
		s.sendError(conn, fmt.Sprintf("invalid exec request: %v", err))
		return
	}

	// Support isolated execution per machine name
	targetRootfs := ""
	if req.Machine != "" && req.Machine != "default" {
		candidates := []string{
			fmt.Sprintf("/var/machines/%s", req.Machine),
			fmt.Sprintf("/run/machines/%s", req.Machine),
			fmt.Sprintf("/workspace/orbstack_free/aerostack_runtime/data/machines/%s/rootfs", req.Machine),
			fmt.Sprintf("/mnt/mac/aerostack_runtime/data/machines/%s/rootfs", req.Machine),
		}
		for _, cand := range candidates {
			if st, err := os.Stat(cand); err == nil && st.IsDir() {
				targetRootfs = cand
				break
			}
		}
	}

	// 2. Prepare command
	var cmd *exec.Cmd
	var rawCmd []string
	if len(req.Command) == 0 {
		shell := "/bin/sh"
		if _, err := os.Stat("/bin/bash"); err == nil {
			shell = "/bin/bash"
		}
		rawCmd = []string{shell}
	} else if len(req.Command) == 1 {
		shell := "/bin/sh"
		if _, err := os.Stat("/bin/bash"); err == nil {
			shell = "/bin/bash"
		}
		rawCmd = []string{shell, "-c", req.Command[0]}
	} else {
		rawCmd = req.Command
	}

	if targetRootfs != "" {
		// Execute inside machine's isolated rootfs namespace
		chrootArgs := append([]string{targetRootfs}, rawCmd...)
		cmd = exec.Command("chroot", chrootArgs...)
	} else {
		cmd = exec.Command(rawCmd[0], rawCmd[1:]...)
	}

	// Set workdir
	if req.WorkDir != "" {
		if _, err := os.Stat(req.WorkDir); err == nil {
			cmd.Dir = req.WorkDir
		}
	}

	// Set environment
	cmd.Env = os.Environ()
	for k, v := range req.Env {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
	}
	if req.Machine != "" {
		cmd.Env = append(cmd.Env, fmt.Sprintf("AERO_MACHINE=%s", req.Machine))
	}
	cmd.Env = append(cmd.Env, "TERM=xterm-256color")

	cmdStdin, err := cmd.StdinPipe()
	if err != nil {
		s.sendError(conn, err.Error())
		return
	}
	cmdStdout, err := cmd.StdoutPipe()
	if err != nil {
		s.sendError(conn, err.Error())
		return
	}
	cmdStderr, err := cmd.StderrPipe()
	if err != nil {
		s.sendError(conn, err.Error())
		return
	}

	if err := cmd.Start(); err != nil {
		s.sendError(conn, err.Error())
		return
	}

	atomic.AddUint64(&s.execCount, 1)

	// 3. Send Success Response
	resp := ExecResponse{
		Started: true,
		PID:     cmd.Process.Pid,
	}
	respBytes, _ := json.Marshal(resp)
	respHdr := make([]byte, 5)
	respHdr[0] = byte(ExecMsgResponse)
	binary.BigEndian.PutUint32(respHdr[1:], uint32(len(respBytes)))
	_, _ = conn.Write(respHdr)
	_, _ = conn.Write(respBytes)

	// 4. Concurrent streaming
	var wg sync.WaitGroup

	// Stream stdout -> conn
	wg.Add(1)
	go func() {
		defer wg.Done()
		buf := make([]byte, 8192)
		for {
			n, err := cmdStdout.Read(buf)
			if n > 0 {
				frame := make([]byte, 5+n)
				frame[0] = byte(ExecMsgDataStdout)
				binary.BigEndian.PutUint32(frame[1:], uint32(n))
				copy(frame[5:], buf[:n])
				if _, werr := conn.Write(frame); werr != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()

	// Stream stderr -> conn
	wg.Add(1)
	go func() {
		defer wg.Done()
		buf := make([]byte, 8192)
		for {
			n, err := cmdStderr.Read(buf)
			if n > 0 {
				frame := make([]byte, 5+n)
				frame[0] = byte(ExecMsgDataStderr)
				binary.BigEndian.PutUint32(frame[1:], uint32(n))
				copy(frame[5:], buf[:n])
				if _, werr := conn.Write(frame); werr != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()

	// Receive stdin from conn -> process
	go func() {
		defer cmdStdin.Close()
		fHdr := make([]byte, 5)
		for {
			if _, err := io.ReadFull(conn, fHdr); err != nil {
				return
			}
			mType := ExecMsgType(fHdr[0])
			pLen := binary.BigEndian.Uint32(fHdr[1:])
			pBuf := make([]byte, pLen)
			if _, err := io.ReadFull(conn, pBuf); err != nil {
				return
			}

			if mType == ExecMsgDataStdin {
				_, _ = cmdStdin.Write(pBuf)
			}
		}
	}()

	// Wait for stdout and stderr to finish streaming before waiting for process completion
	wg.Wait()
	_ = cmd.Wait()

	exitCode := 0
	if cmd.ProcessState != nil {
		exitCode = cmd.ProcessState.ExitCode()
	}

	// Send ExitStatus frame
	exitFrame := make([]byte, 9)
	exitFrame[0] = byte(ExecMsgExitStatus)
	binary.BigEndian.PutUint32(exitFrame[1:], 4)
	binary.BigEndian.PutUint32(exitFrame[5:], uint32(exitCode))
	_, _ = conn.Write(exitFrame)
}

func (s *ExecServer) sendError(conn net.Conn, errMsg string) {
	resp := ExecResponse{
		Started: false,
		Error:   errMsg,
	}
	respBytes, _ := json.Marshal(resp)
	h := make([]byte, 5)
	h[0] = byte(ExecMsgResponse)
	binary.BigEndian.PutUint32(h[1:], uint32(len(respBytes)))
	_, _ = conn.Write(h)
	_, _ = conn.Write(respBytes)
}
