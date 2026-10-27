//go:build !darwin || !cgo

package main

import (
	"net"
	"strings"
	"sync"
)

// MDNSAdvertiser fallback implementation for non-Darwin or pure-Go builds
type MDNSAdvertiser struct {
	records map[string][]net.IP
	mu      sync.Mutex
}

// NewMDNSAdvertiser creates a fallback mDNS advertiser
func NewMDNSAdvertiser() (*MDNSAdvertiser, error) {
	return &MDNSAdvertiser{
		records: make(map[string][]net.IP),
	}, nil
}

// RegisterHostname stores local mDNS hostnames
func (a *MDNSAdvertiser) RegisterHostname(name string, v4, v6 net.IP) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if !strings.HasSuffix(name, ".") {
		name = name + "."
	}

	var ips []net.IP
	if v4 != nil {
		ips = append(ips, v4)
	}
	if v6 != nil {
		ips = append(ips, v6)
	}
	a.records[name] = ips
	return nil
}

// Close cleans up advertiser resources
func (a *MDNSAdvertiser) Close() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.records = make(map[string][]net.IP)
}
