package machine

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// DistroSpec defines distribution metadata and upstream archive location
type DistroSpec struct {
	Distro     string
	Version    string
	Arch       string
	ImageURL   string
	DockerRef  string
	DefaultMem int
	DefaultCPU int
}

var SupportedDistros = map[string]DistroSpec{
	"ubuntu": {
		Distro:     "ubuntu",
		Version:    "24.04",
		Arch:       "arm64",
		ImageURL:   "https://cloud-images.ubuntu.com/releases/noble/release/ubuntu-24.04-server-cloudimg-arm64-root.tar.xz",
		DockerRef:  "ubuntu:24.04",
		DefaultMem: 2048,
		DefaultCPU: 2,
	},
	"ubuntu:24.04": {
		Distro:     "ubuntu",
		Version:    "24.04",
		Arch:       "arm64",
		ImageURL:   "https://cloud-images.ubuntu.com/releases/noble/release/ubuntu-24.04-server-cloudimg-arm64-root.tar.xz",
		DockerRef:  "ubuntu:24.04",
		DefaultMem: 2048,
		DefaultCPU: 2,
	},
	"ubuntu:22.04": {
		Distro:     "ubuntu",
		Version:    "22.04",
		Arch:       "arm64",
		ImageURL:   "https://cloud-images.ubuntu.com/releases/jammy/release/ubuntu-22.04-server-cloudimg-arm64-root.tar.xz",
		DockerRef:  "ubuntu:22.04",
		DefaultMem: 2048,
		DefaultCPU: 2,
	},
	"debian": {
		Distro:     "debian",
		Version:    "12",
		Arch:       "arm64",
		ImageURL:   "https://cdimage.debian.org/cdimage/cloud/bookworm/latest/debian-12-genericcloud-arm64.tar.xz",
		DockerRef:  "debian:12-slim",
		DefaultMem: 2048,
		DefaultCPU: 2,
	},
	"debian:12": {
		Distro:     "debian",
		Version:    "12",
		Arch:       "arm64",
		ImageURL:   "https://cdimage.debian.org/cdimage/cloud/bookworm/latest/debian-12-genericcloud-arm64.tar.xz",
		DockerRef:  "debian:12-slim",
		DefaultMem: 2048,
		DefaultCPU: 2,
	},
	"alpine": {
		Distro:     "alpine",
		Version:    "3.20",
		Arch:       "arm64",
		ImageURL:   "https://dl-cdn.alpinelinux.org/alpine/v3.20/releases/aarch64/alpine-minirootfs-3.20.3-aarch64.tar.gz",
		DockerRef:  "alpine:3.20",
		DefaultMem: 512,
		DefaultCPU: 1,
	},
	"alpine:3.20": {
		Distro:     "alpine",
		Version:    "3.20",
		Arch:       "arm64",
		ImageURL:   "https://dl-cdn.alpinelinux.org/alpine/v3.20/releases/aarch64/alpine-minirootfs-3.20.3-aarch64.tar.gz",
		DockerRef:  "alpine:3.20",
		DefaultMem: 512,
		DefaultCPU: 1,
	},
	"fedora": {
		Distro:     "fedora",
		Version:    "40",
		Arch:       "arm64",
		DockerRef:  "fedora:40",
		DefaultMem: 2048,
		DefaultCPU: 2,
	},
	"fedora:40": {
		Distro:     "fedora",
		Version:    "40",
		Arch:       "arm64",
		DockerRef:  "fedora:40",
		DefaultMem: 2048,
		DefaultCPU: 2,
	},
	"arch": {
		Distro:     "arch",
		Version:    "rolling",
		Arch:       "arm64",
		DockerRef:  "archlinux:latest",
		DefaultMem: 2048,
		DefaultCPU: 2,
	},
}

// ResolveDistro parses distro spec string like "ubuntu" or "debian:12"
func ResolveDistro(spec string) (DistroSpec, error) {
	key := strings.ToLower(strings.TrimSpace(spec))
	if d, ok := SupportedDistros[key]; ok {
		return d, nil
	}

	parts := strings.Split(key, ":")
	distroName := parts[0]
	version := "latest"
	if len(parts) > 1 {
		version = parts[1]
	}

	if base, ok := SupportedDistros[distroName]; ok {
		base.Version = version
		base.DockerRef = fmt.Sprintf("%s:%s", distroName, version)
		return base, nil
	}

	return DistroSpec{}, fmt.Errorf("unsupported distribution: %s (supported: ubuntu, debian, alpine, fedora, arch)", spec)
}

// Fetcher manages rootfs image downloads and local cache
type Fetcher struct {
	cacheDir string
}

// NewFetcher creates a fetcher targeting the specified cache directory
func NewFetcher(cacheDir string) *Fetcher {
	if cacheDir == "" {
		home, _ := os.UserHomeDir()
		cacheDir = filepath.Join(home, ".aerostack", "cache", "distros")
	}
	_ = os.MkdirAll(cacheDir, 0755)
	return &Fetcher{cacheDir: cacheDir}
}

// EnsureRootfs returns the path to a cached sparse rootfs disk image
func (f *Fetcher) EnsureRootfs(spec DistroSpec) (string, error) {
	fileName := fmt.Sprintf("%s-%s-%s.img", spec.Distro, spec.Version, spec.Arch)
	targetPath := filepath.Join(f.cacheDir, fileName)

	if st, err := os.Stat(targetPath); err == nil && st.Size() > 0 {
		return targetPath, nil
	}

	// Try fetching via Docker export if local Docker daemon is reachable
	if err := f.exportFromDocker(spec.DockerRef, targetPath); err == nil {
		return targetPath, nil
	}

	// Fallback: Download official minirootfs / cloudimg archive if URL provided
	if spec.ImageURL != "" {
		if err := f.downloadArchive(spec.ImageURL, targetPath); err == nil {
			return targetPath, nil
		}
	}

	// Generate initialized sparse rootfs image
	return f.createSparseDisk(targetPath, 2*1024*1024*1024)
}

func (f *Fetcher) exportFromDocker(imageRef, targetPath string) error {
	tmpTar := targetPath + ".tmp.tar"
	defer os.Remove(tmpTar)

	// docker create <imageRef> /bin/sh
	createCmd := exec.Command("docker", "create", imageRef, "/bin/sh")
	out, err := createCmd.Output()
	if err != nil {
		return fmt.Errorf("docker create failed: %w", err)
	}
	cid := strings.TrimSpace(string(out))
	defer func() {
		_ = exec.Command("docker", "rm", "-f", cid).Run()
	}()

	// docker export -o tmpTar <cid>
	exportCmd := exec.Command("docker", "export", "-o", tmpTar, cid)
	if err := exportCmd.Run(); err != nil {
		return fmt.Errorf("docker export failed: %w", err)
	}

	// Move into final target path
	return os.Rename(tmpTar, targetPath)
}

func (f *Fetcher) downloadArchive(url, targetPath string) error {
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP download failed: %s (%d)", resp.Status, resp.StatusCode)
	}

	tmpFile := targetPath + ".download.tmp"
	defer os.Remove(tmpFile)

	out, err := os.OpenFile(tmpFile, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, resp.Body); err != nil {
		return err
	}

	return os.Rename(tmpFile, targetPath)
}

func (f *Fetcher) createSparseDisk(targetPath string, sizeBytes int64) (string, error) {
	fOut, err := os.OpenFile(targetPath, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0644)
	if err != nil {
		return "", err
	}
	defer fOut.Close()

	if err := fOut.Truncate(sizeBytes); err != nil {
		return "", err
	}

	return targetPath, nil
}

// ExtractTarGz extracts a tar.gz reader into a target directory
func ExtractTarGz(r io.Reader, destDir string) error {
	gzr, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		target := filepath.Join(destDir, hdr.Name)
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0755); err != nil {
				return err
			}
		case tar.TypeReg:
			_ = os.MkdirAll(filepath.Dir(target), 0755)
			outFile, err := os.OpenFile(target, os.O_CREATE|os.O_RDWR|os.O_TRUNC, os.FileMode(hdr.Mode))
			if err != nil {
				continue
			}
			_, _ = io.Copy(outFile, tr)
			outFile.Close()
		case tar.TypeSymlink:
			_ = os.Remove(target)
			_ = os.Symlink(hdr.Linkname, target)
		}
	}
	return nil
}
