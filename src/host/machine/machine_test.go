package machine

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveDistro(t *testing.T) {
	tests := []struct {
		input       string
		expectedD   string
		expectedV   string
		expectError bool
	}{
		{"ubuntu", "ubuntu", "24.04", false},
		{"ubuntu:24.04", "ubuntu", "24.04", false},
		{"ubuntu:22.04", "ubuntu", "22.04", false},
		{"debian", "debian", "12", false},
		{"debian:12", "debian", "12", false},
		{"alpine", "alpine", "3.20", false},
		{"alpine:3.20", "alpine", "3.20", false},
		{"fedora", "fedora", "40", false},
		{"arch", "arch", "rolling", false},
		{"unknown_os", "", "", true},
	}

	for _, tt := range tests {
		spec, err := ResolveDistro(tt.input)
		if tt.expectError {
			if err == nil {
				t.Errorf("Expected error for %s, got nil", tt.input)
			}
			continue
		}
		if err != nil {
			t.Fatalf("Unexpected error for %s: %v", tt.input, err)
		}
		if spec.Distro != tt.expectedD {
			t.Errorf("Expected distro %s, got %s", tt.expectedD, spec.Distro)
		}
		if spec.Version != tt.expectedV {
			t.Errorf("Expected version %s, got %s", tt.expectedV, spec.Version)
		}
	}
}

func TestMachineManager_Lifecycle(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "aerostack_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	dataDir := filepath.Join(tempDir, "data")
	cacheDir := filepath.Join(tempDir, "cache")

	mgr := NewManager(dataDir, cacheDir)

	// 1. Create Machine
	meta, err := mgr.CreateMachine("test-ubuntu", "ubuntu:24.04", 4, 4096, "32G")
	if err != nil {
		t.Fatalf("CreateMachine failed: %v", err)
	}
	if meta.Name != "test-ubuntu" || meta.Distro != "ubuntu" || meta.State != "STOPPED" {
		t.Fatalf("Invalid initial metadata: %+v", meta)
	}

	// Verify files created
	diskPath := filepath.Join(dataDir, "test-ubuntu", "rootfs.img")
	if _, err := os.Stat(diskPath); err != nil {
		t.Fatalf("Machine disk missing: %v", err)
	}

	// 2. Start Machine
	if err := mgr.StartMachine("test-ubuntu"); err != nil {
		t.Fatalf("StartMachine failed: %v", err)
	}
	meta, err = mgr.GetMachine("test-ubuntu")
	if err != nil || meta.State != "RUNNING" {
		t.Fatalf("Expected RUNNING state, got: %+v (err: %v)", meta, err)
	}

	// 3. Stop Machine
	if err := mgr.StopMachine("test-ubuntu"); err != nil {
		t.Fatalf("StopMachine failed: %v", err)
	}
	meta, err = mgr.GetMachine("test-ubuntu")
	if err != nil || meta.State != "STOPPED" {
		t.Fatalf("Expected STOPPED state, got: %+v", meta)
	}

	// 4. Clone Machine
	cloned, err := mgr.CloneMachine("test-ubuntu", "test-ubuntu-clone")
	if err != nil {
		t.Fatalf("CloneMachine failed: %v", err)
	}
	if cloned.Name != "test-ubuntu-clone" || cloned.Distro != "ubuntu" {
		t.Fatalf("Invalid cloned metadata: %+v", cloned)
	}

	// 5. List Machines
	list, err := mgr.ListMachines()
	if err != nil {
		t.Fatalf("ListMachines failed: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("Expected 2 machines, got %d", len(list))
	}

	// 6. Delete Machine
	if err := mgr.DeleteMachine("test-ubuntu"); err != nil {
		t.Fatalf("DeleteMachine failed: %v", err)
	}
	list, err = mgr.ListMachines()
	if err != nil {
		t.Fatalf("ListMachines after delete failed: %v", err)
	}
	if len(list) != 1 || list[0].Name != "test-ubuntu-clone" {
		t.Fatalf("Expected 1 machine (test-ubuntu-clone), got %+v", list)
	}
}

func TestCloneFile_Sparse(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "aerostack_clone_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	src := filepath.Join(tempDir, "sparse.img")
	dst := filepath.Join(tempDir, "sparse_clone.img")

	// Create 10MB sparse file
	f, err := os.Create(src)
	if err != nil {
		t.Fatalf("Failed to create file: %v", err)
	}
	if err := f.Truncate(10 * 1024 * 1024); err != nil {
		t.Fatalf("Truncate failed: %v", err)
	}
	_, _ = f.WriteAt([]byte("AEROSTACK_MAGIC_HEADER"), 0)
	_, _ = f.WriteAt([]byte("AEROSTACK_MAGIC_FOOTER"), 10*1024*1024-22)
	f.Close()

	if err := CloneFile(src, dst); err != nil {
		t.Fatalf("CloneFile failed: %v", err)
	}

	stDst, err := os.Stat(dst)
	if err != nil {
		t.Fatalf("Stat dst failed: %v", err)
	}
	if stDst.Size() != 10*1024*1024 {
		t.Fatalf("Expected cloned size 10MB, got %d", stDst.Size())
	}

	dstData, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("ReadFile dst failed: %v", err)
	}
	if string(dstData[:22]) != "AEROSTACK_MAGIC_HEADER" {
		t.Fatalf("Header mismatch in cloned file")
	}
}
