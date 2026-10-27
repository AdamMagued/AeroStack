package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

// CoWDriver defines the interface for fast Copy-on-Write snapshotting and cloning
type CoWDriver interface {
	Name() string
	CreateSnapshot(srcPath, dstPath string) error
	DeleteSnapshot(path string) error
	SupportsFastClone(targetDir string) bool
}

// CoWManager manages snapshot drivers and automates CoW provisioning
type CoWManager struct {
	baseDir string
	driver  CoWDriver
	mu      sync.RWMutex
}

// NewCoWManager creates a CoWManager and auto-detects the optimal driver for baseDir
func NewCoWManager(baseDir string) (*CoWManager, error) {
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create base dir %s: %w", baseDir, err)
	}

	driver := DetectBestCoWDriver(baseDir)

	return &CoWManager{
		baseDir: baseDir,
		driver:  driver,
	}, nil
}

// Driver returns the active CoWDriver
func (m *CoWManager) Driver() CoWDriver {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.driver
}

// Clone creates a zero-copy CoW snapshot from src to dst
func (m *CoWManager) Clone(src, dst string) (time.Duration, error) {
	start := time.Now()
	err := m.driver.CreateSnapshot(src, dst)
	dur := time.Since(start)
	return dur, err
}

// Delete removes a snapshot
func (m *CoWManager) Delete(target string) error {
	return m.driver.DeleteSnapshot(target)
}

// --- Driver 1: Darwin APFS clonefile Driver ---

type DarwinAPFSDriver struct{}

func (d *DarwinAPFSDriver) Name() string {
	return "Darwin-APFS-clonefile"
}

func (d *DarwinAPFSDriver) SupportsFastClone(targetDir string) bool {
	if runtime.GOOS != "darwin" {
		return false
	}
	// Test if clonefile works on this volume
	testSrc := filepath.Join(targetDir, ".cow_test_src")
	testDst := filepath.Join(targetDir, ".cow_test_dst")
	_ = os.WriteFile(testSrc, []byte("test"), 0644)
	defer os.Remove(testSrc)
	defer os.Remove(testDst)

	err := DarwinCloneFile(testSrc, testDst)
	return err == nil
}

func (d *DarwinAPFSDriver) CreateSnapshot(srcPath, dstPath string) error {
	return DarwinCloneFile(srcPath, dstPath)
}

func (d *DarwinAPFSDriver) DeleteSnapshot(path string) error {
	return os.RemoveAll(path)
}

// DarwinCloneFile calls the Darwin clonefile(2) syscall via syscall/sys/clonefile
func DarwinCloneFile(src, dst string) error {
	if runtime.GOOS != "darwin" {
		return fmt.Errorf("clonefile only supported on macOS Darwin")
	}

	// clonefile is Darwin syscall 469 (SYS_clonefile)
	// int clonefile(const char *src, const char *dst, int flags);
	// flags = 0
	srcBytes, err := syscall.BytePtrFromString(src)
	if err != nil {
		return err
	}
	dstBytes, err := syscall.BytePtrFromString(dst)
	if err != nil {
		return err
	}

	// Syscall 469 = SYS_CLONEFILE on Darwin
	const SYS_CLONEFILE = 469
	_, _, errno := syscall.Syscall(SYS_CLONEFILE, uintptr(unsafe.Pointer(srcBytes)), uintptr(unsafe.Pointer(dstBytes)), 0)
	if errno != 0 {
		return fmt.Errorf("clonefile syscall failed: %w (errno %d)", errno, errno)
	}
	return nil
}

// --- Driver 2: Btrfs Subvolume Driver (Guest VM) ---

type BtrfsSubvolumeDriver struct{}

func (d *BtrfsSubvolumeDriver) Name() string {
	return "Linux-Btrfs-Subvolume"
}

func (d *BtrfsSubvolumeDriver) SupportsFastClone(targetDir string) bool {
	// Check if btrfs CLI or ioctl is available and directory is on btrfs filesystem
	_, err := exec.LookPath("btrfs")
	if err != nil {
		return false
	}
	// Check filesystem type via statfs if possible
	cmd := exec.Command("btrfs", "subvolume", "show", targetDir)
	return cmd.Run() == nil
}

func (d *BtrfsSubvolumeDriver) CreateSnapshot(srcPath, dstPath string) error {
	// Execute: btrfs subvolume snapshot <srcPath> <dstPath>
	cmd := exec.Command("btrfs", "subvolume", "snapshot", srcPath, dstPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("btrfs snapshot failed: %w (output: %s)", err, string(out))
	}
	return nil
}

func (d *BtrfsSubvolumeDriver) DeleteSnapshot(path string) error {
	cmd := exec.Command("btrfs", "subvolume", "delete", path)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("btrfs subvolume delete failed: %w (output: %s)", err, string(out))
	}
	return nil
}

// --- Driver 3: Linux OverlayFS Driver ---

type OverlayFSDriver struct{}

func (d *OverlayFSDriver) Name() string {
	return "Linux-OverlayFS"
}

func (d *OverlayFSDriver) SupportsFastClone(targetDir string) bool {
	return runtime.GOOS == "linux"
}

func (d *OverlayFSDriver) CreateSnapshot(srcPath, dstPath string) error {
	// Sets up overlay directories: dstPath/upper, dstPath/work, dstPath/merged
	upper := filepath.Join(dstPath, "upper")
	work := filepath.Join(dstPath, "work")
	merged := filepath.Join(dstPath, "merged")

	if err := os.MkdirAll(upper, 0755); err != nil {
		return err
	}
	if err := os.MkdirAll(work, 0755); err != nil {
		return err
	}
	if err := os.MkdirAll(merged, 0755); err != nil {
		return err
	}

	opts := fmt.Sprintf("lowerdir=%s,upperdir=%s,workdir=%s", srcPath, upper, work)
	cmd := exec.Command("mount", "-t", "overlay", "overlay", "-o", opts, merged)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("mount overlay failed: %w (output: %s)", err, string(out))
	}
	return nil
}

func (d *OverlayFSDriver) DeleteSnapshot(path string) error {
	merged := filepath.Join(path, "merged")
	_ = exec.Command("umount", merged).Run()
	return os.RemoveAll(path)
}

// --- Driver 4: Sparse Copy Fallback Driver ---

type SparseCopyDriver struct{}

func (d *SparseCopyDriver) Name() string {
	return "Sparse-Copy-Fallback"
}

func (d *SparseCopyDriver) SupportsFastClone(targetDir string) bool {
	return true // Universal fallback
}

func (d *SparseCopyDriver) CreateSnapshot(srcPath, dstPath string) error {
	// Copies directory or sparse disk image
	info, err := os.Stat(srcPath)
	if err != nil {
		return err
	}

	if info.IsDir() {
		return copyDir(srcPath, dstPath)
	}
	return copySparseFile(srcPath, dstPath)
}

func (d *SparseCopyDriver) DeleteSnapshot(path string) error {
	return os.RemoveAll(path)
}

// DetectBestCoWDriver determines the optimal driver for given path
func DetectBestCoWDriver(targetDir string) CoWDriver {
	// 1. If on Darwin, test APFS clonefile
	if runtime.GOOS == "darwin" {
		apfs := &DarwinAPFSDriver{}
		if apfs.SupportsFastClone(targetDir) {
			return apfs
		}
	}

	// 2. If on Linux, test Btrfs
	if runtime.GOOS == "linux" {
		btrfs := &BtrfsSubvolumeDriver{}
		if btrfs.SupportsFastClone(targetDir) {
			return btrfs
		}

		// 3. Test OverlayFS
		overlay := &OverlayFSDriver{}
		if overlay.SupportsFastClone(targetDir) {
			return overlay
		}
	}

	// 4. Fallback to sparse copy
	return &SparseCopyDriver{}
}

// Helper: copySparseFile
func copySparseFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	buf := make([]byte, 1024*1024)
	for {
		n, rerr := in.Read(buf)
		if n > 0 {
			if _, werr := out.Write(buf[:n]); werr != nil {
				return werr
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return rerr
		}
	}
	return nil
}

// Helper: copyDir
func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}
		return copySparseFile(path, target)
	})
}
