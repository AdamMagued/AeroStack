package main

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os/exec"
	"sync"
	"time"
)

// MsgType identifies the direct exec protocol packet type
type MsgType uint8

const (
	MsgExecRequest  MsgType = 1
	MsgExecResponse MsgType = 2
	MsgDataStdin    MsgType = 3
	MsgDataStdout   MsgType = 4
	MsgDataStderr   MsgType = 5
	MsgWindowResize MsgType = 6
	MsgExitStatus   MsgType = 7
)

// ExecRequest payload sent from host CLI to guest agent
type ExecRequest struct {
	Machine  string            `json:"machine"`
	Command  []string          `json:"command"`
	User     string            `json:"user"`
	WorkDir  string            `json:"work_dir"`
	Env      map[string]string `json:"env"`
	Tty      bool              `json:"tty"`
	Rows     uint16            `json:"rows"`
	Cols     uint16            `json:"cols"`
}

// ExecResponse payload sent from guest agent to host CLI
type ExecResponse struct {
	PID     int    `json:"pid"`
	Started bool   `json:"started"`
	Error   string `json:"error,omitempty"`
}

// ExitStatusResponse reports child termination
type ExitStatusResponse struct {
	ExitCode int `json:"exit_code"`
}

// WindowResizePayload informs guest agent of terminal dimensions
type WindowResizePayload struct {
	Rows uint16 `json:"rows"`
	Cols uint16 `json:"cols"`
}

// DirectExecClient runs on macOS host to execute commands in Linux VM
type DirectExecClient struct {
	socketPath string
}

// NewDirectExecClient creates a direct exec client
func NewDirectExecClient(socketPath string) *DirectExecClient {
	return &DirectExecClient{socketPath: socketPath}
}

// Execute runs a command via the direct vsock agent protocol and returns execution latency & exit code
func (c *DirectExecClient) Execute(req ExecRequest, stdin io.Reader, stdout, stderr io.Writer) (int, time.Duration, error) {
	startTime := time.Now()

	conn, err := net.Dial("unix", c.socketPath)
	if err != nil {
		return -1, 0, fmt.Errorf("failed to connect to vsock agent: %w", err)
	}
	defer conn.Close()

	// 1. Send ExecRequest header
	reqBytes, err := json.Marshal(req)
	if err != nil {
		return -1, 0, err
	}

	header := make([]byte, 5)
	header[0] = byte(MsgExecRequest)
	binary.BigEndian.PutUint32(header[1:], uint32(len(reqBytes)))

	if _, err := conn.Write(header); err != nil {
		return -1, 0, err
	}
	if _, err := conn.Write(reqBytes); err != nil {
		return -1, 0, err
	}

	// 2. Read ExecResponse
	respHeader := make([]byte, 5)
	if _, err := io.ReadFull(conn, respHeader); err != nil {
		return -1, 0, err
	}
	respLen := binary.BigEndian.Uint32(respHeader[1:])
	respBody := make([]byte, respLen)
	if _, err := io.ReadFull(conn, respBody); err != nil {
		return -1, 0, err
	}

	var execResp ExecResponse
	if err := json.Unmarshal(respBody, &execResp); err != nil {
		return -1, 0, err
	}
	if !execResp.Started {
		return -1, 0, fmt.Errorf("exec rejected by agent: %s", execResp.Error)
	}

	// 3. Bidirectional streaming loop
	var wg sync.WaitGroup
	var exitCode int

	// Stdin writer (if reader supplied)
	if stdin != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			buf := make([]byte, 4096)
			for {
				n, err := stdin.Read(buf)
				if n > 0 {
					frame := make([]byte, 5+n)
					frame[0] = byte(MsgDataStdin)
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
	}

	// Read loop from agent (stdout, stderr, exit status)
	for {
		frameHeader := make([]byte, 5)
		if _, err := io.ReadFull(conn, frameHeader); err != nil {
			break
		}
		msgType := MsgType(frameHeader[0])
		payloadLen := binary.BigEndian.Uint32(frameHeader[1:])
		payload := make([]byte, payloadLen)
		if _, err := io.ReadFull(conn, payload); err != nil {
			break
		}

		switch msgType {
		case MsgDataStdout:
			if stdout != nil {
				_, _ = stdout.Write(payload)
			}
		case MsgDataStderr:
			if stderr != nil {
				_, _ = stderr.Write(payload)
			}
		case MsgExitStatus:
			if len(payload) >= 4 {
				exitCode = int(int32(binary.BigEndian.Uint32(payload)))
			}
			goto Done
		}
	}

Done:
	dur := time.Since(startTime)
	return exitCode, dur, nil
}

// GuestExecServer runs inside the Linux VM guest or daemon harness
type GuestExecServer struct {
	listener net.Listener
	mu       sync.Mutex
	stopped  bool
}

// NewGuestExecServer creates a new agent server
func NewGuestExecServer(socketPath string) (*GuestExecServer, error) {
	l, err := net.Listen("unix", socketPath)
	if err != nil {
		return nil, err
	}
	srv := &GuestExecServer{listener: l}
	go srv.serve()
	return srv, nil
}

func (s *GuestExecServer) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopped = true
	return s.listener.Close()
}

func (s *GuestExecServer) serve() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			s.mu.Lock()
			stopped := s.stopped
			s.mu.Unlock()
			if stopped {
				return
			}
			continue
		}
		go s.handleConn(conn)
	}
}

func (s *GuestExecServer) handleConn(conn net.Conn) {
	defer conn.Close()

	// Read initial request
	header := make([]byte, 5)
	if _, err := io.ReadFull(conn, header); err != nil {
		return
	}
	length := binary.BigEndian.Uint32(header[1:])
	body := make([]byte, length)
	if _, err := io.ReadFull(conn, body); err != nil {
		return
	}

	var req ExecRequest
	_ = json.Unmarshal(body, &req)

	// In real guest, runs forkpty/execve or /bin/sh -c
	var cmd *exec.Cmd
	if len(req.Command) == 1 {
		cmd = exec.Command("/bin/sh", "-c", req.Command[0])
	} else if len(req.Command) > 1 {
		cmd = exec.Command(req.Command[0], req.Command[1:]...)
	} else {
		cmd = exec.Command("/bin/sh")
	}

	cmdStdin, _ := cmd.StdinPipe()
	cmdStdout, _ := cmd.StdoutPipe()
	cmdStderr, _ := cmd.StderrPipe()

	if err := cmd.Start(); err != nil {
		resp := ExecResponse{Started: false, Error: err.Error()}
		rBytes, _ := json.Marshal(resp)
		h := make([]byte, 5)
		h[0] = byte(MsgExecResponse)
		binary.BigEndian.PutUint32(h[1:], uint32(len(rBytes)))
		_, _ = conn.Write(h)
		_, _ = conn.Write(rBytes)
		return
	}

	// Send success start response
	resp := ExecResponse{Started: true, PID: cmd.Process.Pid}
	rBytes, _ := json.Marshal(resp)
	h := make([]byte, 5)
	h[0] = byte(MsgExecResponse)
	binary.BigEndian.PutUint32(h[1:], uint32(len(rBytes)))
	_, _ = conn.Write(h)
	_, _ = conn.Write(rBytes)

	// Stream stdout to client
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := cmdStdout.Read(buf)
			if n > 0 {
				frame := make([]byte, 5+n)
				frame[0] = byte(MsgDataStdout)
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

	// Stream stderr to client
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := cmdStderr.Read(buf)
			if n > 0 {
				frame := make([]byte, 5+n)
				frame[0] = byte(MsgDataStderr)
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

	// Receive stdin from client
	go func() {
		defer cmdStdin.Close()
		fHeader := make([]byte, 5)
		for {
			if _, err := io.ReadFull(conn, fHeader); err != nil {
				return
			}
			pLen := binary.BigEndian.Uint32(fHeader[1:])
			pBuf := make([]byte, pLen)
			if _, err := io.ReadFull(conn, pBuf); err != nil {
				return
			}
			if MsgType(fHeader[0]) == MsgDataStdin {
				_, _ = cmdStdin.Write(pBuf)
			}
		}
	}()

	// Wait for process completion
	_ = cmd.Wait()
	exitCode := cmd.ProcessState.ExitCode()

	// Send ExitStatus frame
	exitFrame := make([]byte, 9)
	exitFrame[0] = byte(MsgExitStatus)
	binary.BigEndian.PutUint32(exitFrame[1:], 4)
	binary.BigEndian.PutUint32(exitFrame[5:], uint32(exitCode))
	_, _ = conn.Write(exitFrame)
}
