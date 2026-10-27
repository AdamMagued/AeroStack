package main

import (
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Inotify Protocol constants matching event_protocol.h
const (
	OrbFSMagic   uint32 = 0x4F524246 // "ORBF"
	OrbFSVersion uint16 = 1

	OrbOpEvent  uint16 = 1
	OrbOpPing   uint16 = 2
	OrbOpPong   uint16 = 3
	OrbOpRescan uint16 = 4
	OrbOpAck    uint16 = 5

	OrbInAccess       uint32 = 0x00000001
	OrbInModify       uint32 = 0x00000002
	OrbInAttrib       uint32 = 0x00000004
	OrbInCloseWrite   uint32 = 0x00000008
	OrbInCloseNowrite uint32 = 0x00000010
	OrbInOpen         uint32 = 0x00000020
	OrbInMovedFrom    uint32 = 0x00000040
	OrbInMovedTo      uint32 = 0x00000080
	OrbInCreate       uint32 = 0x00000100
	OrbInDelete       uint32 = 0x00000200
	OrbInDeleteSelf   uint32 = 0x00000400
	OrbInMoveSelf     uint32 = 0x00000800
	OrbInIsDir        uint32 = 0x40000000

	OrbMaxPathLen uint32 = 4096
	HeaderSize    int    = 32
)

// EventHeader represents the 32-byte binary event header
type EventHeader struct {
	Magic       uint32 // OrbFSMagic
	Version     uint16 // OrbFSVersion
	Opcode      uint16 // OrbOp*
	InotifyMask uint32 // OrbIn* bitmask
	Seq         uint64 // Monotonic sequence
	TimestampNS uint64 // Host timestamp
	PathLen     uint32 // Length of relative path
}

// InotifySynthesizerConfig holds configuration for the inotify engine
type InotifySynthesizerConfig struct {
	ListenPort uint32
	MountRoot  string // Root path of VirtioFS mount in guest (e.g. /mnt/mac)
}

// InotifySynthesizer receives file events from host and triggers Linux kernel inotify
type InotifySynthesizer struct {
	config        InotifySynthesizerConfig
	listener      net.Listener
	stopCh        chan struct{}
	wg            sync.WaitGroup
	running       int32
	eventsHandled uint64
	bytesRewritten uint64
	touchesDone   uint64
	mu            sync.Mutex
	activeConns   map[net.Conn]struct{}
}

// NewInotifySynthesizer constructs a new inotify synthesizer
func NewInotifySynthesizer(cfg InotifySynthesizerConfig) *InotifySynthesizer {
	if cfg.ListenPort == 0 {
		cfg.ListenPort = 9922
	}
	if cfg.MountRoot == "" {
		cfg.MountRoot = "/mnt/mac"
	}

	return &InotifySynthesizer{
		config:      cfg,
		stopCh:      make(chan struct{}),
		activeConns: make(map[net.Conn]struct{}),
	}
}

// DecodeEventHeader parses a 32-byte header (LittleEndian)
func DecodeEventHeader(buf []byte) (EventHeader, error) {
	if len(buf) < HeaderSize {
		return EventHeader{}, fmt.Errorf("buffer too short: %d < %d", len(buf), HeaderSize)
	}
	hdr := EventHeader{
		Magic:       binary.LittleEndian.Uint32(buf[0:4]),
		Version:     binary.LittleEndian.Uint16(buf[4:6]),
		Opcode:      binary.LittleEndian.Uint16(buf[6:8]),
		InotifyMask: binary.LittleEndian.Uint32(buf[8:12]),
		Seq:         binary.LittleEndian.Uint64(buf[12:20]),
		TimestampNS: binary.LittleEndian.Uint64(buf[20:28]),
		PathLen:     binary.LittleEndian.Uint32(buf[28:32]),
	}
	if hdr.Magic != OrbFSMagic {
		return hdr, fmt.Errorf("invalid magic: 0x%08X (expected 0x%08X)", hdr.Magic, OrbFSMagic)
	}
	if hdr.PathLen > OrbMaxPathLen {
		return hdr, fmt.Errorf("path length %d exceeds max %d", hdr.PathLen, OrbMaxPathLen)
	}
	return hdr, nil
}

// EncodeEventHeader encodes an EventHeader into a 32-byte buffer
func EncodeEventHeader(hdr EventHeader) []byte {
	buf := make([]byte, HeaderSize)
	binary.LittleEndian.PutUint32(buf[0:4], hdr.Magic)
	binary.LittleEndian.PutUint16(buf[4:6], hdr.Version)
	binary.LittleEndian.PutUint16(buf[6:8], hdr.Opcode)
	binary.LittleEndian.PutUint32(buf[8:12], hdr.InotifyMask)
	binary.LittleEndian.PutUint64(buf[12:20], hdr.Seq)
	binary.LittleEndian.PutUint64(buf[20:28], hdr.TimestampNS)
	binary.LittleEndian.PutUint32(buf[28:32], hdr.PathLen)
	return buf
}

// Start begins listening on vsock port 9922
func (s *InotifySynthesizer) Start() error {
	if !atomic.CompareAndSwapInt32(&s.running, 0, 1) {
		return fmt.Errorf("inotify synthesizer already running")
	}

	l, err := ListenVsock(s.config.ListenPort)
	if err != nil {
		atomic.StoreInt32(&s.running, 0)
		return fmt.Errorf("failed to listen on vsock port %d: %w", s.config.ListenPort, err)
	}
	s.listener = l

	s.wg.Add(1)
	go s.acceptLoop()

	log.Printf("[orb-agent:fs] Inotify synthesizer listening on vsock port %d (MountRoot: %s)",
		s.config.ListenPort, s.config.MountRoot)
	return nil
}

// Stop cleanly terminates the synthesizer
func (s *InotifySynthesizer) Stop() {
	if atomic.CompareAndSwapInt32(&s.running, 1, 0) {
		close(s.stopCh)
		if s.listener != nil {
			_ = s.listener.Close()
		}

		s.mu.Lock()
		for c := range s.activeConns {
			_ = c.Close()
		}
		s.mu.Unlock()

		s.wg.Wait()
		log.Printf("[orb-agent:fs] Inotify synthesizer stopped (Handled: %d events)",
			atomic.LoadUint64(&s.eventsHandled))
	}
}

func (s *InotifySynthesizer) acceptLoop() {
	defer s.wg.Done()
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			if atomic.LoadInt32(&s.running) == 0 {
				return
			}
			continue
		}

		s.mu.Lock()
		s.activeConns[conn] = struct{}{}
		s.mu.Unlock()

		s.wg.Add(1)
		go s.handleConn(conn)
	}
}

func (s *InotifySynthesizer) handleConn(conn net.Conn) {
	defer s.wg.Done()
	defer func() {
		_ = conn.Close()
		s.mu.Lock()
		delete(s.activeConns, conn)
		s.mu.Unlock()
	}()

	hdrBuf := make([]byte, HeaderSize)
	for atomic.LoadInt32(&s.running) == 1 {
		_, err := io.ReadFull(conn, hdrBuf)
		if err != nil {
			return
		}

		hdr, err := DecodeEventHeader(hdrBuf)
		if err != nil {
			continue
		}

		var pathStr string
		if hdr.PathLen > 0 {
			pathBuf := make([]byte, hdr.PathLen)
			if _, err := io.ReadFull(conn, pathBuf); err != nil {
				return
			}
			pathStr = string(pathBuf)
		}

		switch hdr.Opcode {
		case OrbOpPing:
			// Send Pong
			pongHdr := EventHeader{
				Magic:       OrbFSMagic,
				Version:     OrbFSVersion,
				Opcode:      OrbOpPong,
				Seq:         hdr.Seq,
				TimestampNS: uint64(time.Now().UnixNano()),
			}
			_, _ = conn.Write(EncodeEventHeader(pongHdr))

		case OrbOpEvent:
			s.triggerKernelEvent(hdr.InotifyMask, pathStr)
			atomic.AddUint64(&s.eventsHandled, 1)

			// Send ACK if requested
			ackHdr := EventHeader{
				Magic:       OrbFSMagic,
				Version:     OrbFSVersion,
				Opcode:      OrbOpAck,
				Seq:         hdr.Seq,
				TimestampNS: uint64(time.Now().UnixNano()),
			}
			_, _ = conn.Write(EncodeEventHeader(ackHdr))
		}
	}
}

// resolvePath resolves the relative or absolute host path to the guest filesystem path
func (s *InotifySynthesizer) resolvePath(relPath string) string {
	// If path already exists as-is, use it
	if _, err := os.Stat(relPath); err == nil {
		return relPath
	}

	// Try mounting under MountRoot
	cleaned := filepath.Clean("/" + strings.TrimPrefix(relPath, "/"))
	candidate := filepath.Join(s.config.MountRoot, cleaned)
	if _, err := os.Stat(candidate); err == nil {
		return candidate
	}

	// Return candidate even if not yet created (for create events)
	return candidate
}

// triggerKernelEvent executes atomic byte-0 rewrite or touch to synthesize inotify event in guest kernel
func (s *InotifySynthesizer) triggerKernelEvent(mask uint32, relPath string) {
	if relPath == "" {
		return
	}

	targetPath := s.resolvePath(relPath)
	now := time.Now()

	// Engine 1: Non-destructive Byte-0 Rewrite for file modifications
	if mask&(OrbInModify|OrbInCloseWrite) != 0 {
		f, err := os.OpenFile(targetPath, os.O_RDWR, 0644)
		if err == nil {
			var b [1]byte
			n, rerr := f.ReadAt(b[:], 0)
			if rerr == nil && n == 1 {
				// Write back the identical byte to offset 0
				// This enters sys_write -> vfs_write -> fsnotify_modify()
				_, _ = f.WriteAt(b[:], 0)
				atomic.AddUint64(&s.bytesRewritten, 1)
			}
			_ = f.Close()
			return
		}
	}

	// For create, delete, attribute change, or when byte-0 rewrite is not applicable
	// Update file or parent directory timestamps to emit IN_ATTRIB
	if err := os.Chtimes(targetPath, now, now); err == nil {
		atomic.AddUint64(&s.touchesDone, 1)
		return
	}

	// If target does not exist (e.g. deleted or not created yet), touch parent directory
	parentDir := filepath.Dir(targetPath)
	if err := os.Chtimes(parentDir, now, now); err == nil {
		atomic.AddUint64(&s.touchesDone, 1)
	}
}
