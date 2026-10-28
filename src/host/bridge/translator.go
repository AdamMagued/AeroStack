package main

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

// PathTranslator handles bidirectional translation between macOS host paths
// and Linux VM guest paths for Docker volume and bind mounts.
type PathTranslator struct {
	// HostPrefix is the host filesystem prefix (usually "/" on macOS)
	HostPrefix string
	// GuestPrefix is where host filesystem is mounted in guest (e.g. "/mnt/mac" or "/" for 1:1)
	GuestPrefix string
	// CustomMappings maps specific host paths to specific guest paths
	CustomMappings map[string]string
	// ReverseMappings maps guest paths back to host paths
	ReverseMappings map[string]string
}

// NewPathTranslator creates a new translator with given guest mount prefix.
func NewPathTranslator(guestPrefix string) *PathTranslator {
	if guestPrefix == "" {
		guestPrefix = "/mnt/mac"
	}
	guestPrefix = filepath.Clean(guestPrefix)
	if guestPrefix == "." {
		guestPrefix = "/"
	}

	hostPrefix := "/"
	if guestPrefix == "/workspace" {
		hostPrefix = "/Volumes/AdamSSD/antigravity_workspace"
	}

	pt := &PathTranslator{
		HostPrefix:      hostPrefix,
		GuestPrefix:     guestPrefix,
		CustomMappings:  make(map[string]string),
		ReverseMappings: make(map[string]string),
	}

	if guestPrefix == "/workspace" {
		pt.AddMapping("/Volumes/AdamSSD/antigravity_workspace", "/workspace")
		pt.AddMapping("/Volumes/AdamSSD", "/workspace")
		pt.AddMapping("/Users", "/mnt/mac/Users")
	}

	return pt
}

// AddMapping registers an explicit mount mapping (e.g., host /Volumes/SSD -> guest /mnt/ssd)
func (pt *PathTranslator) AddMapping(hostPath, guestPath string) {
	cleanHost := filepath.Clean(hostPath)
	cleanGuest := filepath.Clean(guestPath)
	pt.CustomMappings[cleanHost] = cleanGuest
	if existing, ok := pt.ReverseMappings[cleanGuest]; !ok || len(cleanHost) > len(existing) {
		pt.ReverseMappings[cleanGuest] = cleanHost
	}
}

// HostToGuest translates a macOS host path to Linux VM VirtioFS path.
func (pt *PathTranslator) HostToGuest(hostPath string) string {
	clean := filepath.Clean(hostPath)

	// Docker socket and kernel pseudo filesystems are unmodified
	if clean == "/var/run/docker.sock" || strings.HasPrefix(clean, "/proc") || strings.HasPrefix(clean, "/sys") {
		return clean
	}

	// 1. Check explicit custom mappings (prioritizing longest prefix match)
	var bestHostKey string
	var bestGuestVal string
	for hostKey, guestVal := range pt.CustomMappings {
		if clean == hostKey || strings.HasPrefix(clean, hostKey+"/") {
			if len(hostKey) > len(bestHostKey) {
				bestHostKey = hostKey
				bestGuestVal = guestVal
			}
		}
	}
	if bestHostKey != "" {
		if clean == bestHostKey {
			return bestGuestVal
		}
		rel := strings.TrimPrefix(clean, bestHostKey)
		return filepath.Join(bestGuestVal, rel)
	}

	// 2. Direct 1:1 mirroring mode
	if pt.GuestPrefix == "/" {
		return clean
	}

	// 3. Prefix translation: prepend GuestPrefix to host path
	// e.g. /Users/alice/app -> /mnt/mac/Users/alice/app
	return filepath.Join(pt.GuestPrefix, clean)
}

// GuestToHost translates a Linux VM VirtioFS path back to macOS host path.
func (pt *PathTranslator) GuestToHost(guestPath string) string {
	clean := filepath.Clean(guestPath)

	if clean == "/var/run/docker.sock" {
		return clean
	}

	// 1. Check explicit custom reverse mappings (prioritizing longest prefix match)
	var bestGuestKey string
	var bestHostVal string
	for guestKey, hostVal := range pt.ReverseMappings {
		if clean == guestKey || strings.HasPrefix(clean, guestKey+"/") {
			if len(guestKey) > len(bestGuestKey) {
				bestGuestKey = guestKey
				bestHostVal = hostVal
			}
		}
	}
	if bestGuestKey != "" {
		if clean == bestGuestKey {
			return bestHostVal
		}
		rel := strings.TrimPrefix(clean, bestGuestKey)
		return filepath.Join(bestHostVal, rel)
	}

	// 2. Direct 1:1 mirroring mode
	if pt.GuestPrefix == "/" {
		return clean
	}

	// 3. Prefix reverse translation: strip GuestPrefix
	if strings.HasPrefix(clean, pt.GuestPrefix) {
		rel := strings.TrimPrefix(clean, pt.GuestPrefix)
		if pt.HostPrefix == "/" {
			if !strings.HasPrefix(rel, "/") {
				rel = "/" + rel
			}
			return filepath.Clean(rel)
		}
		return filepath.Join(pt.HostPrefix, rel)
	}

	return clean
}

// TranslateBindMount rewrites a single bind mount spec (host_path:container_path[:options])
func (pt *PathTranslator) TranslateBindMount(bindSpec string, hostToGuest bool) string {
	parts := strings.Split(bindSpec, ":")
	if len(parts) < 2 {
		return bindSpec
	}

	source := parts[0]
	var translatedSource string
	if hostToGuest {
		translatedSource = pt.HostToGuest(source)
	} else {
		translatedSource = pt.GuestToHost(source)
	}

	parts[0] = translatedSource
	return strings.Join(parts, ":")
}

// TranslateContainerCreateRequestBody rewrites host paths in POST /containers/create JSON body
func (pt *PathTranslator) TranslateContainerCreateRequestBody(body []byte) ([]byte, error) {
	if len(body) == 0 {
		return body, nil
	}

	var root map[string]interface{}
	if err := json.Unmarshal(body, &root); err != nil {
		return nil, fmt.Errorf("failed to unmarshal container create json: %w", err)
	}

	// 1. Rewrite top-level Mounts if present
	if rootMountsRaw, ok := root["Mounts"]; ok && rootMountsRaw != nil {
		if mountsSlice, ok := rootMountsRaw.([]interface{}); ok {
			for _, m := range mountsSlice {
				if mountMap, ok := m.(map[string]interface{}); ok {
					mType, _ := mountMap["Type"].(string)
					if mType == "bind" || mType == "" {
						if source, ok := mountMap["Source"].(string); ok {
							mountMap["Source"] = pt.HostToGuest(source)
						}
					}
				}
			}
		}
	}

	hostConfigRaw, ok := root["HostConfig"]
	if ok && hostConfigRaw != nil {
		if hostConfig, ok := hostConfigRaw.(map[string]interface{}); ok {
			// Rewrite Binds: ["/host:/guest[:ro]"]
			if bindsRaw, ok := hostConfig["Binds"]; ok && bindsRaw != nil {
				if bindsSlice, ok := bindsRaw.([]interface{}); ok {
					newBinds := make([]string, 0, len(bindsSlice))
					for _, b := range bindsSlice {
						if bindStr, ok := b.(string); ok {
							newBinds = append(newBinds, pt.TranslateBindMount(bindStr, true))
						}
					}
					hostConfig["Binds"] = newBinds
				}
			}

			// Rewrite Mounts: [{"Type": "bind", "Source": "...", "Target": "..."}]
			if mountsRaw, ok := hostConfig["Mounts"]; ok && mountsRaw != nil {
				if mountsSlice, ok := mountsRaw.([]interface{}); ok {
					for _, m := range mountsSlice {
						if mountMap, ok := m.(map[string]interface{}); ok {
							mType, _ := mountMap["Type"].(string)
							if mType == "bind" || mType == "" {
								if source, ok := mountMap["Source"].(string); ok {
									mountMap["Source"] = pt.HostToGuest(source)
								}
							}
						}
					}
				}
			}

			// Rewrite PortBindings: if HostIp is 127.0.0.1, change to 0.0.0.0 so guest dockerd binds on eth0 for macOS host forwarding
			if pbRaw, ok := hostConfig["PortBindings"]; ok && pbRaw != nil {
				if pbMap, ok := pbRaw.(map[string]interface{}); ok {
					for _, bindings := range pbMap {
						if bList, ok := bindings.([]interface{}); ok {
							for _, b := range bList {
								if bMap, ok := b.(map[string]interface{}); ok {
									if hostIp, ok := bMap["HostIp"].(string); ok && (hostIp == "127.0.0.1" || hostIp == "") {
										bMap["HostIp"] = "0.0.0.0"
									}
								}
							}
						}
					}
				}
			}
		}
	}

	return json.Marshal(root)
}

// TranslateContainerInspectResponseBody rewrites guest paths in GET /containers/{id}/json response
func (pt *PathTranslator) TranslateContainerInspectResponseBody(body []byte) ([]byte, error) {
	if len(body) == 0 {
		return body, nil
	}

	var root map[string]interface{}
	if err := json.Unmarshal(body, &root); err != nil {
		return nil, fmt.Errorf("failed to unmarshal container inspect json: %w", err)
	}

	// 1. Rewrite top-level Mounts
	if mountsRaw, ok := root["Mounts"]; ok && mountsRaw != nil {
		if mountsSlice, ok := mountsRaw.([]interface{}); ok {
			for _, m := range mountsSlice {
				if mountMap, ok := m.(map[string]interface{}); ok {
					mType, _ := mountMap["Type"].(string)
					if mType == "bind" || mType == "" {
						if source, ok := mountMap["Source"].(string); ok {
							mountMap["Source"] = pt.GuestToHost(source)
						}
					}
				}
			}
		}
	}

	// 2. Rewrite HostConfig.Binds
	if hostConfigRaw, ok := root["HostConfig"]; ok && hostConfigRaw != nil {
		if hostConfig, ok := hostConfigRaw.(map[string]interface{}); ok {
			if bindsRaw, ok := hostConfig["Binds"]; ok && bindsRaw != nil {
				if bindsSlice, ok := bindsRaw.([]interface{}); ok {
					newBinds := make([]string, 0, len(bindsSlice))
					for _, b := range bindsSlice {
						if bindStr, ok := b.(string); ok {
							newBinds = append(newBinds, pt.TranslateBindMount(bindStr, false))
						}
					}
					hostConfig["Binds"] = newBinds
				}
			}

			// Also HostConfig.Mounts if present
			if mountsRaw, ok := hostConfig["Mounts"]; ok && mountsRaw != nil {
				if mountsSlice, ok := mountsRaw.([]interface{}); ok {
					for _, m := range mountsSlice {
						if mountMap, ok := m.(map[string]interface{}); ok {
							mType, _ := mountMap["Type"].(string)
							if mType == "bind" || mType == "" {
								if source, ok := mountMap["Source"].(string); ok {
									mountMap["Source"] = pt.GuestToHost(source)
								}
							}
						}
					}
				}
			}
		}
	}

	return json.Marshal(root)
}
