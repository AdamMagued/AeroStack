package main

import (
	"fmt"
	"net"
	"sync"
	"sync/atomic"
)

// RouterConfig holds IP and MAC configurations for the virtual router
type RouterConfig struct {
	RouterMAC  [6]byte
	RouterIPv4 [4]byte
	RouterIPv6 [16]byte
	SubnetIPv4 net.IPNet
	SubnetIPv6 net.IPNet
}

// RouterStats tracks packet counters
type RouterStats struct {
	RxPackets   uint64
	TxPackets   uint64
	RxBytes     uint64
	TxBytes     uint64
	ArpHandled  uint64
	IcmpHandled uint64
	Ipv6Handled uint64
	Dropped     uint64
}

// UserspaceRouter processes raw Ethernet frames to and from the guest VM
type UserspaceRouter struct {
	config RouterConfig
	stats  RouterStats
	mu     sync.RWMutex

	// Container IP -> MAC mappings
	arpTable map[[4]byte][6]byte

	// Packet transmit callback
	txFunc func([]byte) error
}

// NewUserspaceRouter constructs a new userspace router instance
func NewUserspaceRouter(cfg RouterConfig, txFunc func([]byte) error) *UserspaceRouter {
	return &UserspaceRouter{
		config:   cfg,
		arpTable: make(map[[4]byte][6]byte),
		txFunc:   txFunc,
	}
}

// RegisterContainer associates an IP with a MAC address
func (r *UserspaceRouter) RegisterContainer(ip [4]byte, mac [6]byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.arpTable[ip] = mac
}

// Stats returns a snapshot of router statistics
func (r *UserspaceRouter) Stats() RouterStats {
	return RouterStats{
		RxPackets:   atomic.LoadUint64(&r.stats.RxPackets),
		TxPackets:   atomic.LoadUint64(&r.stats.TxPackets),
		RxBytes:     atomic.LoadUint64(&r.stats.RxBytes),
		TxBytes:     atomic.LoadUint64(&r.stats.TxBytes),
		ArpHandled:  atomic.LoadUint64(&r.stats.ArpHandled),
		IcmpHandled: atomic.LoadUint64(&r.stats.IcmpHandled),
		Ipv6Handled: atomic.LoadUint64(&r.stats.Ipv6Handled),
		Dropped:     atomic.LoadUint64(&r.stats.Dropped),
	}
}

// ProcessFrame handles an incoming raw Ethernet frame from the VM
func (r *UserspaceRouter) ProcessFrame(frame []byte) ([]byte, error) {
	atomic.AddUint64(&r.stats.RxPackets, 1)
	atomic.AddUint64(&r.stats.RxBytes, uint64(len(frame)))

	ethHdr, payload, ok := ParseEthernet(frame)
	if !ok {
		atomic.AddUint64(&r.stats.Dropped, 1)
		return nil, fmt.Errorf("truncated ethernet frame")
	}

	switch ethHdr.EtherType {
	case EtherTypeARP:
		return r.handleARP(ethHdr, payload)

	case EtherTypeIPv4:
		return r.handleIPv4(ethHdr, payload)

	case EtherTypeIPv6:
		return r.handleIPv6(ethHdr, payload)

	default:
		atomic.AddUint64(&r.stats.Dropped, 1)
		return nil, nil
	}
}

func (r *UserspaceRouter) handleARP(ethHdr EthernetHeader, payload []byte) ([]byte, error) {
	arp, ok := ParseARP(payload)
	if !ok {
		atomic.AddUint64(&r.stats.Dropped, 1)
		return nil, fmt.Errorf("malformed arp")
	}

	// Learn sender MAC
	r.mu.Lock()
	r.arpTable[arp.SenderIP] = arp.SenderMAC
	r.mu.Unlock()

	if arp.OpCode == ARPOpRequest {
		// Answer ARP requests with RouterMAC
		atomic.AddUint64(&r.stats.ArpHandled, 1)
		replyARP := BuildARPReply(
			arp.SenderMAC,
			r.config.RouterMAC,
			arp.SenderIP,
			arp.TargetIP,
		)
		replyFrame := BuildEthernet(
			ethHdr.SrcMAC,
			r.config.RouterMAC,
			EtherTypeARP,
			replyARP,
		)
		atomic.AddUint64(&r.stats.TxPackets, 1)
		atomic.AddUint64(&r.stats.TxBytes, uint64(len(replyFrame)))
		return replyFrame, nil
	}

	return nil, nil
}

func (r *UserspaceRouter) handleIPv4(ethHdr EthernetHeader, payload []byte) ([]byte, error) {
	ipHdr, ipPayload, ok := ParseIPv4(payload)
	if !ok {
		atomic.AddUint64(&r.stats.Dropped, 1)
		return nil, fmt.Errorf("malformed ipv4")
	}

	// ICMP Echo (ping) handling directly in userspace for < 1ms response
	if ipHdr.Protocol == IPProtoICMP && len(ipPayload) >= 8 && ipPayload[0] == ICMPTypeEchoRequest {
		atomic.AddUint64(&r.stats.IcmpHandled, 1)
		icmpReply := BuildICMPEchoReply(ipPayload)
		if icmpReply == nil {
			return nil, nil
		}

		replyIPv4 := BuildIPv4(ipHdr.DstIP, ipHdr.SrcIP, IPProtoICMP, icmpReply)
		replyFrame := BuildEthernet(ethHdr.SrcMAC, r.config.RouterMAC, EtherTypeIPv4, replyIPv4)

		atomic.AddUint64(&r.stats.TxPackets, 1)
		atomic.AddUint64(&r.stats.TxBytes, uint64(len(replyFrame)))
		return replyFrame, nil
	}

	return nil, nil
}

func (r *UserspaceRouter) handleIPv6(ethHdr EthernetHeader, payload []byte) ([]byte, error) {
	ip6Hdr, ip6Payload, ok := ParseIPv6(payload)
	if !ok {
		atomic.AddUint64(&r.stats.Dropped, 1)
		return nil, fmt.Errorf("malformed ipv6")
	}

	atomic.AddUint64(&r.stats.Ipv6Handled, 1)

	// ICMPv6 Echo Request handling
	if ip6Hdr.NextHeader == IPProtoICMPv6 && len(ip6Payload) >= 8 && ip6Payload[0] == ICMPv6TypeEchoRequest {
		replyICMPv6 := make([]byte, len(ip6Payload))
		copy(replyICMPv6, ip6Payload)
		replyICMPv6[0] = ICMPv6TypeEchoReply
		replyICMPv6[2] = 0
		replyICMPv6[3] = 0

		// IPv6 pseudo-header for ICMPv6 checksum calculation
		pseudo := make([]byte, 40+len(replyICMPv6))
		copy(pseudo[0:16], ip6Hdr.DstIP[:])
		copy(pseudo[16:32], ip6Hdr.SrcIP[:])
		pseudo[35] = uint8(len(replyICMPv6))
		pseudo[39] = IPProtoICMPv6
		copy(pseudo[40:], replyICMPv6)
		csum := CalculateChecksum(pseudo)
		replyICMPv6[2] = uint8(csum >> 8)
		replyICMPv6[3] = uint8(csum & 0xFF)

		replyHdr := make([]byte, 40)
		replyHdr[0] = 0x60 // IPv6
		replyHdr[4] = uint8(len(replyICMPv6) >> 8)
		replyHdr[5] = uint8(len(replyICMPv6) & 0xFF)
		replyHdr[6] = IPProtoICMPv6
		replyHdr[7] = 64
		copy(replyHdr[8:24], ip6Hdr.DstIP[:])
		copy(replyHdr[24:40], ip6Hdr.SrcIP[:])

		fullIPv6 := append(replyHdr, replyICMPv6...)
		replyFrame := BuildEthernet(ethHdr.SrcMAC, r.config.RouterMAC, EtherTypeIPv6, fullIPv6)

		atomic.AddUint64(&r.stats.TxPackets, 1)
		atomic.AddUint64(&r.stats.TxBytes, uint64(len(replyFrame)))
		return replyFrame, nil
	}

	return nil, nil
}
