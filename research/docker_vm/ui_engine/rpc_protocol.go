package main

import "encoding/json"

// RPCRequest follows JSON-RPC 2.0 specification
type RPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// RPCResponse follows JSON-RPC 2.0 specification
type RPCResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id"`
	Result  interface{} `json:"result,omitempty"`
	Error   *RPCError   `json:"error,omitempty"`
}

// RPCError represents standard JSON-RPC 2.0 error
type RPCError struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

// Standard JSON-RPC Error Codes
const (
	ErrParseError     = -32700
	ErrInvalidRequest = -32600
	ErrMethodNotFound = -32601
	ErrInvalidParams  = -32602
	ErrInternalError  = -32603
)

// UI DTOs

type VMSummary struct {
	Name      string `json:"name"`
	Distro    string `json:"distro"`
	Version   string `json:"version"`
	Arch      string `json:"arch"`
	State     string `json:"state"` // RUNNING, STOPPED
	CPUs      int    `json:"cpus"`
	MemoryMB  int    `json:"memory_mb"`
	IPAddress string `json:"ip_address"`
	Size      string `json:"size"`
	UptimeSec int64  `json:"uptime_sec"`
}

type ContainerSummary struct {
	ID      string            `json:"id"`
	Names   []string          `json:"names"`
	Image   string            `json:"image"`
	Command string            `json:"command"`
	State   string            `json:"state"`
	Status  string            `json:"status"`
	Ports   []string          `json:"ports"`
	Mounts  map[string]string `json:"mounts"`
}

type LiveMetrics struct {
	Timestamp       int64   `json:"timestamp"`
	HostCPUPercent  float64 `json:"host_cpu_percent"`
	GuestCPUPercent float64 `json:"guest_cpu_percent"`
	MemoryUsedMB    int64   `json:"memory_used_mb"`
	MemoryTotalMB   int64   `json:"memory_total_mb"`
	BalloonFreedMB  int64   `json:"balloon_freed_mb"`
	VirtioFSOpsPerS int64   `json:"virtiofs_ops_per_sec"`
	VirtioFSHitRate float64 `json:"virtiofs_cache_hit_rate"`
	NetworkRxKBps   float64 `json:"network_rx_kbps"`
	NetworkTxKBps   float64 `json:"network_tx_kbps"`
}

type OrbSettings struct {
	MaxCPUs         int    `json:"max_cpus"`
	MaxMemoryMB     int    `json:"max_memory_mb"`
	MaxDiskGB       int    `json:"max_disk_gb"`
	RosettaX86      bool   `json:"rosetta_x86"`
	FastVirtioFS    bool   `json:"fast_virtiofs"`
	BatterySaver    bool   `json:"battery_saver"`
	AutoStart       bool   `json:"auto_start"`
	DNSDomain       string `json:"dns_domain"`
	StorageLocation string `json:"storage_location"`
}
