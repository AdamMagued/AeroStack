package machine

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// MachineMetadata represents persisted machine configuration and state
type MachineMetadata struct {
	Name      string    `json:"name"`
	Distro    string    `json:"distro"`
	Version   string    `json:"version"`
	Arch      string    `json:"arch"`
	CPUs      int       `json:"cpus"`
	MemoryMB  int       `json:"memory_mb"`
	DiskSize  string    `json:"disk_size"`
	DiskBytes int64     `json:"disk_bytes"`
	State     string    `json:"state"`
	IPAddress string    `json:"ip"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Manager orchestrates multi-distro microVM machine life cycle and CoW storage
type Manager struct {
	dataDir  string
	fetcher  *Fetcher
	mu       sync.RWMutex
}

// NewManager initializes a machine manager rooted in ~/.aerostack/data/machines
func NewManager(dataDir, cacheDir string) *Manager {
	if dataDir == "" {
		home, _ := os.UserHomeDir()
		dataDir = filepath.Join(home, ".aerostack", "data", "machines")
	}
	_ = os.MkdirAll(dataDir, 0755)
	return &Manager{
		dataDir: dataDir,
		fetcher: NewFetcher(cacheDir),
	}
}

// CreateMachine provisions a new machine from a base rootfs image using CoW cloning
func (m *Manager) CreateMachine(name, distroSpec string, cpus, memoryMB int, diskLimit string) (*MachineMetadata, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if name == "" {
		return nil, fmt.Errorf("machine name cannot be empty")
	}

	machineDir := filepath.Join(m.dataDir, name)
	if _, err := os.Stat(machineDir); err == nil {
		return nil, fmt.Errorf("machine already exists: %s", name)
	}

	spec, err := ResolveDistro(distroSpec)
	if err != nil {
		return nil, err
	}

	if cpus <= 0 {
		cpus = spec.DefaultCPU
	}
	if memoryMB <= 0 {
		memoryMB = spec.DefaultMem
	}
	if diskLimit == "" {
		diskLimit = "32G"
	}

	// 1. Ensure base rootfs image exists in cache
	baseImagePath, err := m.fetcher.EnsureRootfs(spec)
	if err != nil {
		return nil, fmt.Errorf("failed to obtain base rootfs: %w", err)
	}

	// 2. Create machine directory
	if err := os.MkdirAll(machineDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create machine directory: %w", err)
	}

	// 3. Clone base rootfs image into machine rootfs.img via Darwin clonefile / sparse copy
	targetDiskPath := filepath.Join(machineDir, "rootfs.img")
	if err := CloneFile(baseImagePath, targetDiskPath); err != nil {
		_ = os.RemoveAll(machineDir)
		return nil, fmt.Errorf("failed to clone rootfs image: %w", err)
	}

	// 4. Calculate disk bytes
	var diskBytes int64
	if st, err := os.Stat(targetDiskPath); err == nil {
		diskBytes = st.Size()
	}

	now := time.Now().UTC()
	meta := &MachineMetadata{
		Name:      name,
		Distro:    spec.Distro,
		Version:   spec.Version,
		Arch:      spec.Arch,
		CPUs:      cpus,
		MemoryMB:  memoryMB,
		DiskSize:  diskLimit,
		DiskBytes: diskBytes,
		State:     "STOPPED",
		IPAddress: "-",
		CreatedAt: now,
		UpdatedAt: now,
	}

	if err := m.saveMetadata(name, meta); err != nil {
		_ = os.RemoveAll(machineDir)
		return nil, err
	}

	return meta, nil
}

// ListMachines returns all discovered machines from the filesystem
func (m *Manager) ListMachines() ([]MachineMetadata, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	entries, err := os.ReadDir(m.dataDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var results []MachineMetadata
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		meta, err := m.loadMetadata(entry.Name())
		if err == nil && meta != nil {
			results = append(results, *meta)
		}
	}
	return results, nil
}

// GetMachine returns metadata for a single machine
func (m *Manager) GetMachine(name string) (*MachineMetadata, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.loadMetadata(name)
}

// StartMachine transitions a machine state to RUNNING
func (m *Manager) StartMachine(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	meta, err := m.loadMetadata(name)
	if err != nil {
		return err
	}

	meta.State = "RUNNING"
	meta.IPAddress = "192.168.128.2"
	meta.UpdatedAt = time.Now().UTC()
	return m.saveMetadata(name, meta)
}

// StopMachine transitions a machine state to STOPPED
func (m *Manager) StopMachine(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	meta, err := m.loadMetadata(name)
	if err != nil {
		return err
	}

	meta.State = "STOPPED"
	meta.IPAddress = "-"
	meta.UpdatedAt = time.Now().UTC()
	return m.saveMetadata(name, meta)
}

// DeleteMachine purges a machine directory and disk
func (m *Manager) DeleteMachine(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	machineDir := filepath.Join(m.dataDir, name)
	if _, err := os.Stat(machineDir); os.IsNotExist(err) {
		return fmt.Errorf("machine not found: %s", name)
	}

	return os.RemoveAll(machineDir)
}

// CloneMachine creates an instant copy of a machine using APFS CoW clonefile
func (m *Manager) CloneMachine(source, destination string) (*MachineMetadata, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	srcDir := filepath.Join(m.dataDir, source)
	if _, err := os.Stat(srcDir); os.IsNotExist(err) {
		return nil, fmt.Errorf("source machine not found: %s", source)
	}

	dstDir := filepath.Join(m.dataDir, destination)
	if _, err := os.Stat(dstDir); err == nil {
		return nil, fmt.Errorf("destination machine already exists: %s", destination)
	}

	srcMeta, err := m.loadMetadata(source)
	if err != nil {
		return nil, fmt.Errorf("failed to read source metadata: %w", err)
	}

	if err := os.MkdirAll(dstDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create destination directory: %w", err)
	}

	srcDisk := filepath.Join(srcDir, "rootfs.img")
	dstDisk := filepath.Join(dstDir, "rootfs.img")
	if err := CloneFile(srcDisk, dstDisk); err != nil {
		_ = os.RemoveAll(dstDir)
		return nil, fmt.Errorf("failed to clone disk: %w", err)
	}

	now := time.Now().UTC()
	dstMeta := &MachineMetadata{
		Name:      destination,
		Distro:    srcMeta.Distro,
		Version:   srcMeta.Version,
		Arch:      srcMeta.Arch,
		CPUs:      srcMeta.CPUs,
		MemoryMB:  srcMeta.MemoryMB,
		DiskSize:  srcMeta.DiskSize,
		DiskBytes: srcMeta.DiskBytes,
		State:     "STOPPED",
		IPAddress: "-",
		CreatedAt: now,
		UpdatedAt: now,
	}

	if err := m.saveMetadata(destination, dstMeta); err != nil {
		_ = os.RemoveAll(dstDir)
		return nil, err
	}

	return dstMeta, nil
}

func (m *Manager) loadMetadata(name string) (*MachineMetadata, error) {
	metaPath := filepath.Join(m.dataDir, name, "metadata.json")
	data, err := os.ReadFile(metaPath)
	if err != nil {
		return nil, err
	}

	var meta MachineMetadata
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, err
	}
	return &meta, nil
}

func (m *Manager) saveMetadata(name string, meta *MachineMetadata) error {
	metaPath := filepath.Join(m.dataDir, name, "metadata.json")
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(metaPath, data, 0644)
}
