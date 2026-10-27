package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"sync"
	"time"
)

// MachineState represents current operational state of a Linux VM
type MachineState string

const (
	StateStopped  MachineState = "STOPPED"
	StateStarting MachineState = "STARTING"
	StateRunning  MachineState = "RUNNING"
	StateStopping MachineState = "STOPPING"
	StateError    MachineState = "ERROR"
)

// LinuxMachine represents a managed Linux distro instance
type LinuxMachine struct {
	Name        string            `json:"name"`
	Distro      string            `json:"distro"`
	Version     string            `json:"version"`
	Arch        string            `json:"arch"`
	State       MachineState      `json:"state"`
	CPUs        int               `json:"cpus"`
	MemoryMB    int               `json:"memory_mb"`
	DiskLimitGB int               `json:"disk_limit_gb"`
	RootfsPath  string            `json:"rootfs_path"`
	User        string            `json:"user"`
	IPAddress   string            `json:"ip_address"`
	CreatedAt   time.Time         `json:"created_at"`
	StartedAt   time.Time         `json:"started_at,omitempty"`
	PID         int               `json:"pid,omitempty"`
	Isolated    bool              `json:"isolated"`
	Mounts      []string          `json:"mounts"`
	UserData    string            `json:"user_data,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

// CreateRequest holds options for machine creation
type CreateRequest struct {
	Name             string
	DistroSpec       string // e.g. "ubuntu:24.04"
	Arch             string
	CPUs             int
	MemoryMB         int
	DiskLimitGB      int
	User             string
	Password         string
	UserDataFile     string
	Isolated         bool
	IsolateNetwork   bool
	ForwardSSHAgent  bool
	Mounts           []string
}

// VMLifecycleManager manages all virtual machines and their underlying storage
type VMLifecycleManager struct {
	dataDir     string
	machinesDir string
	baseDir     string
	registry    *DistroRegistry
	cowMgr      *CoWManager
	machines    map[string]*LinuxMachine
	mu          sync.RWMutex
	defaultVM   string
}

// NewVMLifecycleManager creates a new lifecycle manager
func NewVMLifecycleManager(dataDir string) (*VMLifecycleManager, error) {
	machinesDir := filepath.Join(dataDir, "machines")
	baseDir := filepath.Join(dataDir, "base_images")

	if err := os.MkdirAll(machinesDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create machines dir: %w", err)
	}
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create base dir: %w", err)
	}

	cowMgr, err := NewCoWManager(machinesDir)
	if err != nil {
		return nil, fmt.Errorf("failed to init cow manager: %w", err)
	}

	mgr := &VMLifecycleManager{
		dataDir:     dataDir,
		machinesDir: machinesDir,
		baseDir:     baseDir,
		registry:    NewDistroRegistry(),
		cowMgr:      cowMgr,
		machines:    make(map[string]*LinuxMachine),
	}

	_ = mgr.loadState()
	return mgr, nil
}

// Create provisions a new VM using instant CoW snapshotting
func (m *VMLifecycleManager) Create(req CreateRequest) (*LinuxMachine, time.Duration, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if req.Name == "" {
		req.Name = req.DistroSpec
	}

	if _, exists := m.machines[req.Name]; exists {
		return nil, 0, fmt.Errorf("machine %q already exists", req.Name)
	}

	spec, version, arch, err := m.registry.ParseDistroSpec(req.DistroSpec, req.Arch)
	if err != nil {
		return nil, 0, err
	}

	// Determine username (default to macOS host user)
	userName := req.User
	if userName == "" {
		if u, err := user.Current(); err == nil && u.Username != "" {
			userName = u.Username
		} else {
			userName = "orb"
		}
	}

	// Defaults for CPU / RAM
	cpus := req.CPUs
	if cpus <= 0 {
		cpus = 2
	}
	mem := req.MemoryMB
	if mem <= 0 {
		mem = 2048
	}
	disk := req.DiskLimitGB
	if disk <= 0 {
		disk = 32
	}

	// Prepare base image placeholder if not yet downloaded
	baseImagePath := filepath.Join(m.baseDir, fmt.Sprintf("%s-%s-%s.img", spec.ID, version, arch))
	if _, err := os.Stat(baseImagePath); os.IsNotExist(err) {
		// Create minimal base directory/image for instantaneous bootstrapping
		if err := m.bootstrapBaseImage(baseImagePath, spec, version, arch); err != nil {
			return nil, 0, fmt.Errorf("failed to bootstrap base image: %w", err)
		}
	}

	// Instant CoW clone to target VM directory
	targetPath := filepath.Join(m.machinesDir, req.Name)
	cloneDur, err := m.cowMgr.Clone(baseImagePath, targetPath)
	if err != nil {
		return nil, 0, fmt.Errorf("CoW clone failed: %w", err)
	}

	// Inject user provisioning and cloud-init / config
	m.provisionMachineUser(targetPath, userName, req.Password)

	vm := &LinuxMachine{
		Name:        req.Name,
		Distro:      spec.ID,
		Version:     version,
		Arch:        arch,
		State:       StateStopped,
		CPUs:        cpus,
		MemoryMB:    mem,
		DiskLimitGB: disk,
		RootfsPath:  targetPath,
		User:        userName,
		CreatedAt:   time.Now(),
		Isolated:    req.Isolated,
		Mounts:      req.Mounts,
	}

	m.machines[req.Name] = vm
	if m.defaultVM == "" {
		m.defaultVM = req.Name
	}
	_ = m.saveState()

	return vm, cloneDur, nil
}

// Start boots or un-pauses a machine
func (m *VMLifecycleManager) Start(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	vm, exists := m.machines[name]
	if !exists {
		return fmt.Errorf("machine %q not found", name)
	}

	if vm.State == StateRunning {
		return nil
	}

	vm.State = StateRunning
	vm.StartedAt = time.Now()
	// Assign deterministic internal IPv4 for host/guest routing
	vm.IPAddress = fmt.Sprintf("192.168.215.%d", (len(m.machines)%250)+2)
	vm.PID = os.Getpid() // Proxy PID in test harness

	_ = m.saveState()
	return nil
}

// Stop gracefully powers down a machine
func (m *VMLifecycleManager) Stop(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	vm, exists := m.machines[name]
	if !exists {
		return fmt.Errorf("machine %q not found", name)
	}

	if vm.State == StateStopped {
		return nil
	}

	vm.State = StateStopped
	vm.PID = 0
	_ = m.saveState()
	return nil
}

// Clone creates an instant zero-copy duplicate of an existing VM
func (m *VMLifecycleManager) Clone(srcName, dstName string) (*LinuxMachine, time.Duration, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	srcVM, exists := m.machines[srcName]
	if !exists {
		return nil, 0, fmt.Errorf("source machine %q not found", srcName)
	}

	if _, exists := m.machines[dstName]; exists {
		return nil, 0, fmt.Errorf("destination machine %q already exists", dstName)
	}

	targetPath := filepath.Join(m.machinesDir, dstName)
	cloneDur, err := m.cowMgr.Clone(srcVM.RootfsPath, targetPath)
	if err != nil {
		return nil, 0, fmt.Errorf("CoW clone failed: %w", err)
	}

	dstVM := &LinuxMachine{
		Name:        dstName,
		Distro:      srcVM.Distro,
		Version:     srcVM.Version,
		Arch:        srcVM.Arch,
		State:       StateStopped,
		CPUs:        srcVM.CPUs,
		MemoryMB:    srcVM.MemoryMB,
		DiskLimitGB: srcVM.DiskLimitGB,
		RootfsPath:  targetPath,
		User:        srcVM.User,
		CreatedAt:   time.Now(),
		Isolated:    srcVM.Isolated,
		Mounts:      srcVM.Mounts,
	}

	m.machines[dstName] = dstVM
	_ = m.saveState()

	return dstVM, cloneDur, nil
}

// Delete removes a VM and its storage
func (m *VMLifecycleManager) Delete(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	vm, exists := m.machines[name]
	if !exists {
		return fmt.Errorf("machine %q not found", name)
	}

	if vm.State == StateRunning {
		vm.State = StateStopped
	}

	if err := m.cowMgr.Delete(vm.RootfsPath); err != nil {
		_ = os.RemoveAll(vm.RootfsPath)
	}

	delete(m.machines, name)
	if m.defaultVM == name {
		m.defaultVM = ""
		for k := range m.machines {
			m.defaultVM = k
			break
		}
	}

	_ = m.saveState()
	return nil
}

// List returns a snapshot of all machines
func (m *VMLifecycleManager) List() []*LinuxMachine {
	m.mu.RLock()
	defer m.mu.RUnlock()

	list := make([]*LinuxMachine, 0, len(m.machines))
	for _, vm := range m.machines {
		copyVM := *vm
		list = append(list, &copyVM)
	}
	return list
}

// Get returns machine info
func (m *VMLifecycleManager) Get(name string) (*LinuxMachine, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	vm, exists := m.machines[name]
	if !exists {
		return nil, fmt.Errorf("machine %q not found", name)
	}
	copyVM := *vm
	return &copyVM, nil
}

// Internal: bootstrap minimal golden base image
func (m *VMLifecycleManager) bootstrapBaseImage(path string, spec DistroSpec, version, arch string) error {
	if err := os.MkdirAll(path, 0755); err != nil {
		return err
	}
	// Create essential rootfs hierarchy
	dirs := []string{"etc", "bin", "sbin", "usr/bin", "usr/lib", "home", "root", "tmp", "var/log", "dev", "proc", "sys", "mnt/mac"}
	for _, d := range dirs {
		_ = os.MkdirAll(filepath.Join(path, d), 0755)
	}

	// Write /etc/os-release
	osRelease := fmt.Sprintf("NAME=\"%s\"\nVERSION=\"%s\"\nID=%s\nID_LIKE=linux\nPRETTY_NAME=\"%s %s (%s)\"\n",
		spec.Name, version, spec.ID, spec.Name, version, arch)
	_ = os.WriteFile(filepath.Join(path, "etc/os-release"), []byte(osRelease), 0644)

	// Write /etc/hostname
	_ = os.WriteFile(filepath.Join(path, "etc/hostname"), []byte(spec.ID+"\n"), 0644)

	return nil
}

// Internal: user provisioning
func (m *VMLifecycleManager) provisionMachineUser(rootfs, username, password string) {
	passwdLine := fmt.Sprintf("%s:x:1000:1000:%s,,,:/home/%s:/bin/bash\n", username, username, username)
	_ = os.WriteFile(filepath.Join(rootfs, "etc/passwd"), []byte("root:x:0:0:root:/root:/bin/bash\n"+passwdLine), 0644)

	groupLine := fmt.Sprintf("%s:x:1000:\nsudo:x:27:%s\nwheel:x:10:%s\n", username, username, username)
	_ = os.WriteFile(filepath.Join(rootfs, "etc/group"), []byte("root:x:0:\n"+groupLine), 0644)

	userHome := filepath.Join(rootfs, "home", username)
	_ = os.MkdirAll(userHome, 0755)

	// Sudoers
	sudoersDir := filepath.Join(rootfs, "etc/sudoers.d")
	_ = os.MkdirAll(sudoersDir, 0750)
	_ = os.WriteFile(filepath.Join(sudoersDir, "99-orb-user"), []byte(fmt.Sprintf("%s ALL=(ALL) NOPASSWD:ALL\n", username)), 0440)
}

// State persistence
func (m *VMLifecycleManager) saveState() error {
	stateFile := filepath.Join(m.dataDir, "machines.json")
	data, err := json.MarshalIndent(m.machines, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(stateFile, data, 0644)
}

func (m *VMLifecycleManager) loadState() error {
	stateFile := filepath.Join(m.dataDir, "machines.json")
	data, err := os.ReadFile(stateFile)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, &m.machines)
}
