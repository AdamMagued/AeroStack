package main

import (
	"fmt"
	"runtime"
	"strings"
)

// DistroSpec defines configuration and rootfs endpoints for a Linux distribution
type DistroSpec struct {
	ID             string            `json:"id"`
	Name           string            `json:"name"`
	DefaultVersion string            `json:"default_version"`
	SupportedArchs []string          `json:"supported_archs"`
	Versions       map[string]string `json:"versions"` // version alias -> canonical
	RootfsTemplate string            `json:"rootfs_template"`
	DefaultShell   string            `json:"default_shell"`
	InitSystem     string            `json:"init_system"` // systemd, openrc, runit
	PackageMgr     string            `json:"package_mgr"`
}

// DistroRegistry manages supported Linux distributions and architectures
type DistroRegistry struct {
	distros map[string]DistroSpec
}

// NewDistroRegistry initializes catalog with OrbStack parity distributions
func NewDistroRegistry() *DistroRegistry {
	reg := &DistroRegistry{
		distros: make(map[string]DistroSpec),
	}

	// Ubuntu
	reg.distros["ubuntu"] = DistroSpec{
		ID:             "ubuntu",
		Name:           "Ubuntu",
		DefaultVersion: "noble",
		SupportedArchs: []string{"arm64", "amd64"},
		Versions: map[string]string{
			"24.04":    "noble",
			"noble":    "noble",
			"22.04":    "jammy",
			"jammy":    "jammy",
			"20.04":    "focal",
			"focal":    "focal",
			"resolute": "resolute",
		},
		RootfsTemplate: "https://cloud-images.ubuntu.com/%s/current/%s-server-cloudimg-%s-root.tar.xz",
		DefaultShell:   "/bin/bash",
		InitSystem:     "systemd",
		PackageMgr:     "apt",
	}

	// Debian
	reg.distros["debian"] = DistroSpec{
		ID:             "debian",
		Name:           "Debian GNU/Linux",
		DefaultVersion: "bookworm",
		SupportedArchs: []string{"arm64", "amd64"},
		Versions: map[string]string{
			"12":       "bookworm",
			"bookworm": "bookworm",
			"11":       "bullseye",
			"bullseye": "bullseye",
			"13":       "trixie",
			"trixie":   "trixie",
			"sid":      "sid",
		},
		RootfsTemplate: "https://images.linuxcontainers.org/images/debian/%s/%s/default/rootfs.tar.xz",
		DefaultShell:   "/bin/bash",
		InitSystem:     "systemd",
		PackageMgr:     "apt",
	}

	// Alpine
	reg.distros["alpine"] = DistroSpec{
		ID:             "alpine",
		Name:           "Alpine Linux",
		DefaultVersion: "3.20",
		SupportedArchs: []string{"arm64", "amd64"},
		Versions: map[string]string{
			"3.20": "3.20",
			"3.21": "3.21",
			"3.19": "3.19",
			"edge": "edge",
		},
		RootfsTemplate: "https://dl-cdn.alpinelinux.org/alpine/%s/releases/%s/alpine-minirootfs-%s-%s.tar.gz",
		DefaultShell:   "/bin/sh",
		InitSystem:     "openrc",
		PackageMgr:     "apk",
	}

	// Arch Linux
	reg.distros["arch"] = DistroSpec{
		ID:             "arch",
		Name:           "Arch Linux",
		DefaultVersion: "current",
		SupportedArchs: []string{"arm64", "amd64"},
		Versions: map[string]string{
			"current": "current",
			"latest":  "current",
		},
		RootfsTemplate: "https://images.linuxcontainers.org/images/archlinux/current/%s/default/rootfs.tar.xz",
		DefaultShell:   "/bin/bash",
		InitSystem:     "systemd",
		PackageMgr:     "pacman",
	}

	// Fedora
	reg.distros["fedora"] = DistroSpec{
		ID:             "fedora",
		Name:           "Fedora Linux",
		DefaultVersion: "40",
		SupportedArchs: []string{"arm64", "amd64"},
		Versions: map[string]string{
			"40": "40",
			"41": "41",
			"39": "39",
			"rawhide": "rawhide",
		},
		RootfsTemplate: "https://images.linuxcontainers.org/images/fedora/%s/%s/default/rootfs.tar.xz",
		DefaultShell:   "/bin/bash",
		InitSystem:     "systemd",
		PackageMgr:     "dnf",
	}

	return reg
}

// ParseDistroSpec parses strings like "ubuntu", "ubuntu:24.04", "alpine:edge"
func (r *DistroRegistry) ParseDistroSpec(specStr, overrideArch string) (DistroSpec, string, string, error) {
	parts := strings.Split(specStr, ":")
	distroID := strings.ToLower(parts[0])

	spec, exists := r.distros[distroID]
	if !exists {
		return DistroSpec{}, "", "", fmt.Errorf("unknown distro %q. Supported distros: ubuntu, debian, alpine, arch, fedora", distroID)
	}

	version := spec.DefaultVersion
	if len(parts) > 1 && parts[1] != "" {
		requestedVer := parts[1]
		if canonical, ok := spec.Versions[requestedVer]; ok {
			version = canonical
		} else {
			version = requestedVer
		}
	}

	arch := overrideArch
	if arch == "" {
		if runtime.GOARCH == "arm64" {
			arch = "arm64"
		} else {
			arch = "amd64"
		}
	}

	// Validate arch support
	supported := false
	for _, a := range spec.SupportedArchs {
		if a == arch {
			supported = true
			break
		}
	}
	if !supported {
		return DistroSpec{}, "", "", fmt.Errorf("distro %s does not support architecture %s", distroID, arch)
	}

	return spec, version, arch, nil
}

// ListDistros returns all registered distros
func (r *DistroRegistry) ListDistros() []DistroSpec {
	list := make([]DistroSpec, 0, len(r.distros))
	for _, spec := range r.distros {
		list = append(list, spec)
	}
	return list
}
