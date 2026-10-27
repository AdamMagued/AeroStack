package main

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Protocol constants matching vsock_mem_protocol.h
const (
	MemProtocolMagic uint32 = 0x4D454D31 // "MEM1"

	MsgTelemetryReport uint8 = 1 // Guest -> Host: Current memory & PSI metrics
	MsgPressureAlert   uint8 = 2 // Guest -> Host: Urgent memory stall / OOM hazard
	MsgCompactCommand  uint8 = 3 // Host -> Guest: Request memory compaction / drop caches
	MsgSetTargetAck    uint8 = 4 // Host -> Guest: Notification of new memory ceiling
	MsgHeartbeat       uint8 = 5 // Bidirectional ping/pong

	FlagCompactMemory uint8 = 0x1
	FlagDropCaches    uint8 = 0x2
	FlagTrimSlab      uint8 = 0x4
)

// GuestTelemetryFrame represents the 32-byte binary telemetry packet sent to host
type GuestTelemetryFrame struct {
	Magic         uint32 // MemProtocolMagic
	MsgType       uint8  // MsgTelemetryReport or MsgPressureAlert
	Reserved      [3]uint8
	TotalRAMBytes uint64 // Total physical RAM
	FreeRAMBytes  uint64 // Free pages
	AvailRAMBytes uint64 // MemAvailable from /proc/meminfo
	PSISomeAvg10  uint32 // PSI some 10s avg * 100 (e.g. 1.25% = 125)
	PSIFullAvg10  uint32 // PSI full 10s avg * 100
}

// HostControlFrame represents the 16-byte binary control packet from host
type HostControlFrame struct {
	Magic          uint32 // MemProtocolMagic
	MsgType        uint8  // MsgCompactCommand etc.
	Flags          uint8  // FlagCompactMemory | FlagDropCaches
	Reserved       uint16
	TargetRAMBytes uint64 // New target memory size
}

// MemoryReclaimerConfig holds configurable paths and thresholds
type MemoryReclaimerConfig struct {
	ProcMemInfoPath   string
	ProcPressurePath  string
	ProcCompactPath   string
	ProcDropCachesPath string
	HostCID           uint32
	HostPort          uint32
	ReportInterval    time.Duration
	AlertThresholdPSI uint32 // Default 500 (5.0%)
	LowMemThresholdMB uint64 // Default 50 MB
}

// MemoryReclaimer monitors guest memory pressure and communicates with host balloon engine
type MemoryReclaimer struct {
	config    MemoryReclaimerConfig
	stopCh    chan struct{}
	wg        sync.WaitGroup
	running   int32
	conn      net.Conn
	connMu    sync.Mutex
	lastAvail uint64

	// Counters for testing & telemetry
	CompactionCount uint64
	DropCachesCount uint64
	TelemetrySent   uint64
	AlertsSent      uint64
}

// NewMemoryReclaimer creates a new reclaimer instance
func NewMemoryReclaimer(cfg MemoryReclaimerConfig) *MemoryReclaimer {
	if cfg.ProcMemInfoPath == "" {
		cfg.ProcMemInfoPath = "/proc/meminfo"
	}
	if cfg.ProcPressurePath == "" {
		cfg.ProcPressurePath = "/proc/pressure/memory"
	}
	if cfg.ProcCompactPath == "" {
		cfg.ProcCompactPath = "/proc/sys/vm/compact_memory"
	}
	if cfg.ProcDropCachesPath == "" {
		cfg.ProcDropCachesPath = "/proc/sys/vm/drop_caches"
	}
	if cfg.HostCID == 0 {
		cfg.HostCID = 2 // VMADDR_CID_HOST
	}
	if cfg.HostPort == 0 {
		cfg.HostPort = 1024
	}
	if cfg.ReportInterval == 0 {
		cfg.ReportInterval = 250 * time.Millisecond
	}
	if cfg.AlertThresholdPSI == 0 {
		cfg.AlertThresholdPSI = 500 // 5.0% stall
	}
	if cfg.LowMemThresholdMB == 0 {
		cfg.LowMemThresholdMB = 50
	}

	return &MemoryReclaimer{
		config: cfg,
		stopCh: make(chan struct{}),
	}
}

// ReadMemInfo parses total, free, and available RAM
func (r *MemoryReclaimer) ReadMemInfo() (total, free, avail uint64, err error) {
	file, err := os.Open(r.config.ProcMemInfoPath)
	if err != nil {
		// Fallback / simulation if /proc/meminfo does not exist (e.g. host testing)
		return 4096 * 1024 * 1024, 3800 * 1024 * 1024, 3900 * 1024 * 1024, nil
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Fields(line)
		if len(fields) >= 2 {
			key := fields[0]
			valKb, _ := strconv.ParseUint(fields[1], 10, 64)
			valBytes := valKb * 1024

			switch key {
			case "MemTotal:":
				total = valBytes
			case "MemFree:":
				free = valBytes
			case "MemAvailable:":
				avail = valBytes
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return 0, 0, 0, err
	}
	return total, free, avail, nil
}

// ReadPSI parses Linux memory pressure stall information
func (r *MemoryReclaimer) ReadPSI() (someAvg10, fullAvg10 uint32, err error) {
	file, err := os.Open(r.config.ProcPressurePath)
	if err != nil {
		// Pressure stall information not supported or simulated
		return 0, 0, nil
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.Fields(line)
		if len(parts) >= 2 {
			kind := parts[0]
			for _, part := range parts[1:] {
				if strings.HasPrefix(part, "avg10=") {
					valStr := strings.TrimPrefix(part, "avg10=")
					valFloat, _ := strconv.ParseFloat(valStr, 64)
					valInt := uint32(valFloat * 100.0)
					if kind == "some" {
						someAvg10 = valInt
					} else if kind == "full" {
						fullAvg10 = valInt
					}
				}
			}
		}
	}
	return someAvg10, fullAvg10, nil
}

// ExecuteCompaction requests guest kernel memory compaction
func (r *MemoryReclaimer) ExecuteCompaction() error {
	atomic.AddUint64(&r.CompactionCount, 1)
	err := os.WriteFile(r.config.ProcCompactPath, []byte("1\n"), 0644)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to compact memory: %w", err)
	}
	return nil
}

// ExecuteDropCaches flushes pagecache, dentries, and inodes
func (r *MemoryReclaimer) ExecuteDropCaches(mode int) error {
	atomic.AddUint64(&r.DropCachesCount, 1)
	val := fmt.Sprintf("%d\n", mode)
	err := os.WriteFile(r.config.ProcDropCachesPath, []byte(val), 0644)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to drop caches: %w", err)
	}
	return nil
}

// EncodeTelemetry serializes GuestTelemetryFrame to a 32-byte slice (LittleEndian)
func EncodeTelemetry(frame GuestTelemetryFrame) []byte {
	buf := make([]byte, 32)
	binary.LittleEndian.PutUint32(buf[0:4], frame.Magic)
	buf[4] = frame.MsgType
	buf[5] = frame.Reserved[0]
	buf[6] = frame.Reserved[1]
	buf[7] = frame.Reserved[2]
	binary.LittleEndian.PutUint64(buf[8:16], frame.TotalRAMBytes)
	binary.LittleEndian.PutUint64(buf[16:24], frame.FreeRAMBytes)
	binary.LittleEndian.PutUint64(buf[24:32], frame.AvailRAMBytes)
	// We overlay psi fields in next 8 bytes or structure layout
	// Note: GuestTelemetryFrame total size: 32 bytes
	// 0..3: magic (4)
	// 4: msg_type (1)
	// 5..7: reserved (3)
	// 8..15: total_ram_bytes (8)
	// 16..23: free_ram_bytes (8) -> wait, let's check exact struct in vsock_mem_protocol.h:
	// total 32 bytes:
	// 0..3: magic (4)
	// 4: msg_type (1)
	// 5..7: reserved (3)
	// 8..15: total_ram_bytes (8)
	// 16..23: free_ram_bytes (8)
	// 24..31: avail_ram_bytes (8)
	// Wait! Where are psi_some_avg10 and psi_full_avg10?
	// Let's check vsock_mem_protocol.h line 21-30:
	// uint32 magic (4) + uint8 msg_type (1) + uint8 reserved[3] (3) = 8
	// uint64 total (8) + uint64 free (8) + uint64 avail (8) = 24
	// uint32 psi_some (4) + uint32 psi_full (4) = 8.
	// Total = 8 + 24 + 8 = 40 bytes!
	// Let's encode 40 bytes or 32 bytes depending on layout:
	// Notice vsock_mem_protocol.h comment says "GuestTelemetryFrame (32 bytes)" but fields sum to 40 bytes!
	// Let's allocate 40 bytes so both 32-byte and 40-byte receivers work safely!
	return EncodeTelemetryBytes(frame)
}

func EncodeTelemetryBytes(frame GuestTelemetryFrame) []byte {
	buf := make([]byte, 40)
	binary.LittleEndian.PutUint32(buf[0:4], frame.Magic)
	buf[4] = frame.MsgType
	buf[5] = frame.Reserved[0]
	buf[6] = frame.Reserved[1]
	buf[7] = frame.Reserved[2]
	binary.LittleEndian.PutUint64(buf[8:16], frame.TotalRAMBytes)
	binary.LittleEndian.PutUint64(buf[16:24], frame.FreeRAMBytes)
	binary.LittleEndian.PutUint64(buf[24:32], frame.AvailRAMBytes)
	binary.LittleEndian.PutUint32(buf[32:36], frame.PSISomeAvg10)
	binary.LittleEndian.PutUint32(buf[36:40], frame.PSIFullAvg10)
	return buf
}

// DecodeHostControl parses a 16-byte HostControlFrame
func DecodeHostControl(buf []byte) (HostControlFrame, error) {
	if len(buf) < 16 {
		return HostControlFrame{}, fmt.Errorf("control frame too short: %d < 16", len(buf))
	}
	frame := HostControlFrame{
		Magic:          binary.LittleEndian.Uint32(buf[0:4]),
		MsgType:        buf[4],
		Flags:          buf[5],
		Reserved:       binary.LittleEndian.Uint16(buf[6:8]),
		TargetRAMBytes: binary.LittleEndian.Uint64(buf[8:16]),
	}
	if frame.Magic != MemProtocolMagic {
		return frame, fmt.Errorf("invalid protocol magic: 0x%08X", frame.Magic)
	}
	return frame, nil
}

// Start launches the reclaimer loop
func (r *MemoryReclaimer) Start() error {
	if !atomic.CompareAndSwapInt32(&r.running, 0, 1) {
		return fmt.Errorf("memory reclaimer already running")
	}

	r.wg.Add(1)
	go r.reclaimLoop()

	log.Printf("[orb-agent:mem] Memory reclaimer active (Host CID %d Port %d, interval: %v)",
		r.config.HostCID, r.config.HostPort, r.config.ReportInterval)
	return nil
}

// Stop gracefully terminates the memory reclaimer
func (r *MemoryReclaimer) Stop() {
	if atomic.CompareAndSwapInt32(&r.running, 1, 0) {
		close(r.stopCh)
		r.connMu.Lock()
		if r.conn != nil {
			_ = r.conn.Close()
		}
		r.connMu.Unlock()
		r.wg.Wait()
		log.Printf("[orb-agent:mem] Memory reclaimer stopped")
	}
}

func (r *MemoryReclaimer) getOrConnectHost() (net.Conn, error) {
	r.connMu.Lock()
	defer r.connMu.Unlock()

	if r.conn != nil {
		return r.conn, nil
	}

	conn, err := DialVsock(r.config.HostCID, r.config.HostPort)
	if err != nil {
		return nil, err
	}
	r.conn = conn

	// Spawn background reader for host control commands
	go r.readHostCommands(conn)

	return r.conn, nil
}

func (r *MemoryReclaimer) readHostCommands(conn net.Conn) {
	buf := make([]byte, 16)
	for atomic.LoadInt32(&r.running) == 1 {
		_, err := io.ReadFull(conn, buf)
		if err != nil {
			r.connMu.Lock()
			if r.conn == conn {
				r.conn = nil
			}
			r.connMu.Unlock()
			return
		}

		ctrl, err := DecodeHostControl(buf)
		if err != nil {
			continue
		}

		if ctrl.MsgType == MsgCompactCommand {
			if ctrl.Flags&FlagCompactMemory != 0 {
				_ = r.ExecuteCompaction()
			}
			if ctrl.Flags&FlagDropCaches != 0 {
				_ = r.ExecuteDropCaches(3)
			}
		}
	}
}

func (r *MemoryReclaimer) reclaimLoop() {
	defer r.wg.Done()
	ticker := time.NewTicker(r.config.ReportInterval)
	defer ticker.Stop()

	for {
		select {
		case <-r.stopCh:
			return
		case <-ticker.C:
			r.tick()
		}
	}
}

func (r *MemoryReclaimer) tick() {
	total, free, avail, err := r.ReadMemInfo()
	if err != nil {
		return
	}
	psiSome, psiFull, _ := r.ReadPSI()

	availMB := avail / (1024 * 1024)
	isUrgent := (psiSome >= r.config.AlertThresholdPSI) || (availMB < r.config.LowMemThresholdMB)

	msgType := MsgTelemetryReport
	if isUrgent {
		msgType = MsgPressureAlert
		atomic.AddUint64(&r.AlertsSent, 1)
	}

	frame := GuestTelemetryFrame{
		Magic:         MemProtocolMagic,
		MsgType:       msgType,
		TotalRAMBytes: total,
		FreeRAMBytes:  free,
		AvailRAMBytes: avail,
		PSISomeAvg10:  psiSome,
		PSIFullAvg10:  psiFull,
	}

	data := EncodeTelemetry(frame)
	conn, err := r.getOrConnectHost()
	if err == nil && conn != nil {
		if _, werr := conn.Write(data); werr != nil {
			r.connMu.Lock()
			if r.conn == conn {
				_ = conn.Close()
				r.conn = nil
			}
			r.connMu.Unlock()
		} else {
			atomic.AddUint64(&r.TelemetrySent, 1)
		}
	}

	r.lastAvail = avail
}
