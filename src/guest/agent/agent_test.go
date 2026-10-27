package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMemoryReclaimer_Protocol(t *testing.T) {
	frame := GuestTelemetryFrame{
		Magic:         MemProtocolMagic,
		MsgType:       MsgTelemetryReport,
		TotalRAMBytes: 8192 * 1024 * 1024,
		FreeRAMBytes:  4096 * 1024 * 1024,
		AvailRAMBytes: 5120 * 1024 * 1024,
		PSISomeAvg10:  125, // 1.25%
		PSIFullAvg10:  25,  // 0.25%
	}

	encoded := EncodeTelemetry(frame)
	if len(encoded) < 32 {
		t.Fatalf("Encoded telemetry length %d < 32", len(encoded))
	}

	magic := binary.LittleEndian.Uint32(encoded[0:4])
	if magic != MemProtocolMagic {
		t.Fatalf("Unexpected magic: 0x%08X", magic)
	}

	msgType := encoded[4]
	if msgType != MsgTelemetryReport {
		t.Fatalf("Unexpected msgType: %d", msgType)
	}

	total := binary.LittleEndian.Uint64(encoded[8:16])
	if total != frame.TotalRAMBytes {
		t.Fatalf("Total RAM mismatch: %d != %d", total, frame.TotalRAMBytes)
	}

	// Test HostControlFrame decode
	ctrlBuf := make([]byte, 16)
	binary.LittleEndian.PutUint32(ctrlBuf[0:4], MemProtocolMagic)
	ctrlBuf[4] = MsgCompactCommand
	ctrlBuf[5] = FlagCompactMemory | FlagDropCaches
	binary.LittleEndian.PutUint64(ctrlBuf[8:16], 2048*1024*1024)

	ctrl, err := DecodeHostControl(ctrlBuf)
	if err != nil {
		t.Fatalf("Failed to decode host control: %v", err)
	}
	if ctrl.MsgType != MsgCompactCommand {
		t.Fatalf("Expected MsgCompactCommand, got %d", ctrl.MsgType)
	}
	if ctrl.Flags&(FlagCompactMemory|FlagDropCaches) != (FlagCompactMemory | FlagDropCaches) {
		t.Fatalf("Expected flags 0x3, got 0x%x", ctrl.Flags)
	}
	if ctrl.TargetRAMBytes != 2048*1024*1024 {
		t.Fatalf("Target RAM mismatch: %d", ctrl.TargetRAMBytes)
	}
}

func TestMemoryReclaimer_MockExecution(t *testing.T) {
	tmpDir := t.TempDir()
	compactPath := filepath.Join(tmpDir, "compact_memory")
	dropPath := filepath.Join(tmpDir, "drop_caches")

	reclaimer := NewMemoryReclaimer(MemoryReclaimerConfig{
		ProcCompactPath:    compactPath,
		ProcDropCachesPath: dropPath,
	})

	if err := reclaimer.ExecuteCompaction(); err != nil {
		t.Fatalf("Compaction failed: %v", err)
	}
	compactContent, err := os.ReadFile(compactPath)
	if err != nil || string(compactContent) != "1\n" {
		t.Fatalf("Unexpected compact content: %s", string(compactContent))
	}

	if err := reclaimer.ExecuteDropCaches(3); err != nil {
		t.Fatalf("Drop caches failed: %v", err)
	}
	dropContent, err := os.ReadFile(dropPath)
	if err != nil || string(dropContent) != "3\n" {
		t.Fatalf("Unexpected drop content: %s", string(dropContent))
	}
}

func TestInotifySynthesizer_ProtocolAndByte0Rewrite(t *testing.T) {
	tmpDir := t.TempDir()
	testFilePath := filepath.Join(tmpDir, "test.txt")
	originalData := []byte("hello, orbstack!")
	if err := os.WriteFile(testFilePath, originalData, 0644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	hdr := EventHeader{
		Magic:       OrbFSMagic,
		Version:     OrbFSVersion,
		Opcode:      OrbOpEvent,
		InotifyMask: OrbInModify | OrbInCloseWrite,
		Seq:         42,
		TimestampNS: uint64(time.Now().UnixNano()),
		PathLen:     uint32(len(testFilePath)),
	}

	encHdr := EncodeEventHeader(hdr)
	decHdr, err := DecodeEventHeader(encHdr)
	if err != nil {
		t.Fatalf("Failed to decode header: %v", err)
	}
	if decHdr.Seq != 42 || decHdr.InotifyMask != (OrbInModify|OrbInCloseWrite) {
		t.Fatalf("Header mismatch: %+v", decHdr)
	}

	synth := NewInotifySynthesizer(InotifySynthesizerConfig{
		MountRoot: tmpDir,
	})

	// Execute triggerKernelEvent
	synth.triggerKernelEvent(OrbInModify, testFilePath)

	// Verify file content is 100% byte-for-byte identical
	readBack, err := os.ReadFile(testFilePath)
	if err != nil {
		t.Fatalf("Failed to read file: %v", err)
	}
	if !bytes.Equal(readBack, originalData) {
		t.Fatalf("File data corrupted by byte-0 rewrite: %q vs %q", readBack, originalData)
	}
	if synth.bytesRewritten != 1 {
		t.Fatalf("Expected 1 byte rewrite, got %d", synth.bytesRewritten)
	}
}

func TestExecServer_Execution(t *testing.T) {
	// Start exec server on loopback
	server := NewExecServer(ExecServerConfig{ListenPort: 0})
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to listen: %v", err)
	}
	server.listener = l
	server.running = 1
	server.wg.Add(1)
	go server.acceptLoop()
	defer server.Stop()

	// Connect as client
	conn, err := net.Dial("tcp", l.Addr().String())
	if err != nil {
		t.Fatalf("Failed to dial exec server: %v", err)
	}
	defer conn.Close()

	// Send ExecRequest for "echo 'orbstack_exec_ok'"
	req := ExecRequest{
		Command: []string{"echo", "orbstack_exec_ok"},
	}
	reqBytes, _ := json.Marshal(req)
	hdr := make([]byte, 5)
	hdr[0] = byte(ExecMsgRequest)
	binary.BigEndian.PutUint32(hdr[1:], uint32(len(reqBytes)))
	_, _ = conn.Write(hdr)
	_, _ = conn.Write(reqBytes)

	// Read ExecResponse
	respHdr := make([]byte, 5)
	if _, err := io.ReadFull(conn, respHdr); err != nil {
		t.Fatalf("Failed to read resp header: %v", err)
	}
	respLen := binary.BigEndian.Uint32(respHdr[1:])
	respBody := make([]byte, respLen)
	if _, err := io.ReadFull(conn, respBody); err != nil {
		t.Fatalf("Failed to read resp body: %v", err)
	}
	var execResp ExecResponse
	if err := json.Unmarshal(respBody, &execResp); err != nil {
		t.Fatalf("Failed to unmarshal resp: %v", err)
	}
	if !execResp.Started {
		t.Fatalf("Exec not started: %s", execResp.Error)
	}

	// Read frames until exit status
	var stdoutBuf bytes.Buffer
	var exitCode int
	for {
		frameHdr := make([]byte, 5)
		if _, err := io.ReadFull(conn, frameHdr); err != nil {
			break
		}
		mType := ExecMsgType(frameHdr[0])
		pLen := binary.BigEndian.Uint32(frameHdr[1:])
		pBuf := make([]byte, pLen)
		if _, err := io.ReadFull(conn, pBuf); err != nil {
			break
		}

		if mType == ExecMsgDataStdout {
			stdoutBuf.Write(pBuf)
		} else if mType == ExecMsgExitStatus {
			exitCode = int(int32(binary.BigEndian.Uint32(pBuf)))
			break
		}
	}

	if exitCode != 0 {
		t.Fatalf("Expected exit code 0, got %d", exitCode)
	}
	if !bytes.Contains(stdoutBuf.Bytes(), []byte("orbstack_exec_ok")) {
		t.Fatalf("Stdout did not contain expected text: %s", stdoutBuf.String())
	}
}

func TestDockerForwarder_Proxying(t *testing.T) {
	mockSock := fmt.Sprintf("/tmp/m_dk_%d.sock", time.Now().UnixNano()%1000000)
	defer os.Remove(mockSock)

	// Create mock docker socket server
	mockBackend, err := net.Listen("unix", mockSock)
	if err != nil {
		t.Fatalf("Failed to listen on mock socket: %v", err)
	}
	defer mockBackend.Close()

	go func() {
		for {
			conn, err := mockBackend.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				buf := make([]byte, 1024)
				n, _ := c.Read(buf)
				if n > 0 {
					_, _ = c.Write([]byte("HTTP/1.1 200 OK\r\n\r\nOK"))
				}
			}(conn)
		}
	}()

	// Start docker forwarder on loopback
	fwd := NewDockerForwarder(DockerForwarderConfig{
		DockerSockPath: mockSock,
	})
	fwdListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to listen fwd: %v", err)
	}
	fwd.listener = fwdListener
	fwd.running = 1
	fwd.wg.Add(1)
	go fwd.acceptLoop()
	defer fwd.Stop()

	// Dial forwarder
	client, err := net.Dial("tcp", fwdListener.Addr().String())
	if err != nil {
		t.Fatalf("Failed to dial forwarder: %v", err)
	}
	defer client.Close()

	_, _ = client.Write([]byte("GET /v1.45/_ping HTTP/1.1\r\n\r\n"))
	respBuf := make([]byte, 1024)
	n, err := client.Read(respBuf)
	if err != nil {
		t.Fatalf("Failed to read from client: %v", err)
	}
	if !bytes.Contains(respBuf[:n], []byte("200 OK")) {
		t.Fatalf("Expected 200 OK, got: %s", string(respBuf[:n]))
	}
}
