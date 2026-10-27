package main

import (
	"encoding/binary"
	"net"
)

// Ethernet EtherTypes
const (
	EtherTypeIPv4 = 0x0800
	EtherTypeARP  = 0x0806
	EtherTypeIPv6 = 0x86DD
)

// IP Protocols
const (
	IPProtoICMP   = 1
	IPProtoTCP    = 6
	IPProtoUDP    = 17
	IPProtoICMPv6 = 58
)

// ARP Constants
const (
	ARPOpRequest = 1
	ARPOpReply   = 2
)

// ICMP Constants
const (
	ICMPTypeEchoReply   = 0
	ICMPTypeEchoRequest = 8
)

// ICMPv6 Constants
const (
	ICMPv6TypeEchoRequest = 128
	ICMPv6TypeEchoReply   = 129
)

// EthernetHeader represents a 14-byte IEEE 802.3 frame header
type EthernetHeader struct {
	DestMAC   [6]byte
	SrcMAC    [6]byte
	EtherType uint16
}

// ParseEthernet extracts Ethernet header and payload
func ParseEthernet(data []byte) (hdr EthernetHeader, payload []byte, ok bool) {
	if len(data) < 14 {
		return hdr, nil, false
	}
	copy(hdr.DestMAC[:], data[0:6])
	copy(hdr.SrcMAC[:], data[6:12])
	hdr.EtherType = binary.BigEndian.Uint16(data[12:14])
	return hdr, data[14:], true
}

// BuildEthernet constructs a 14-byte Ethernet frame
func BuildEthernet(destMAC, srcMAC [6]byte, etherType uint16, payload []byte) []byte {
	out := make([]byte, 14+len(payload))
	copy(out[0:6], destMAC[:])
	copy(out[6:12], srcMAC[:])
	binary.BigEndian.PutUint16(out[12:14], etherType)
	copy(out[14:], payload)
	return out
}

// ARPHeader represents a 28-byte ARP packet for IPv4 over Ethernet
type ARPHeader struct {
	HardwareType uint16
	ProtocolType uint16
	HardwareSize uint8
	ProtocolSize uint8
	OpCode       uint16
	SenderMAC    [6]byte
	SenderIP     [4]byte
	TargetMAC    [6]byte
	TargetIP     [4]byte
}

// ParseARP parses ARP payload
func ParseARP(data []byte) (arp ARPHeader, ok bool) {
	if len(data) < 28 {
		return arp, false
	}
	arp.HardwareType = binary.BigEndian.Uint16(data[0:2])
	arp.ProtocolType = binary.BigEndian.Uint16(data[2:4])
	arp.HardwareSize = data[4]
	arp.ProtocolSize = data[5]
	arp.OpCode = binary.BigEndian.Uint16(data[6:8])
	copy(arp.SenderMAC[:], data[8:14])
	copy(arp.SenderIP[:], data[14:18])
	copy(arp.TargetMAC[:], data[18:24])
	copy(arp.TargetIP[:], data[24:28])
	return arp, true
}

// BuildARPReply constructs an ARP reply packet
func BuildARPReply(targetMAC, senderMAC [6]byte, targetIP, senderIP [4]byte) []byte {
	out := make([]byte, 28)
	binary.BigEndian.PutUint16(out[0:2], 1)      // Ethernet
	binary.BigEndian.PutUint16(out[2:4], 0x0800) // IPv4
	out[4] = 6                                  // MAC len
	out[5] = 4                                  // IP len
	binary.BigEndian.PutUint16(out[6:8], ARPOpReply)
	copy(out[8:14], senderMAC[:])
	copy(out[14:18], senderIP[:])
	copy(out[18:24], targetMAC[:])
	copy(out[24:28], targetIP[:])
	return out
}

// IPv4Header represents a standard 20-byte IPv4 header
type IPv4Header struct {
	VersionIHL uint8
	TOS        uint8
	TotalLen   uint16
	ID         uint16
	FlagsFrag  uint16
	TTL        uint8
	Protocol   uint8
	Checksum   uint16
	SrcIP      [4]byte
	DstIP      [4]byte
}

// ParseIPv4 parses IPv4 header and payload
func ParseIPv4(data []byte) (hdr IPv4Header, payload []byte, ok bool) {
	if len(data) < 20 {
		return hdr, nil, false
	}
	hdr.VersionIHL = data[0]
	ihl := int(hdr.VersionIHL&0x0F) * 4
	if ihl < 20 || len(data) < ihl {
		return hdr, nil, false
	}
	hdr.TOS = data[1]
	hdr.TotalLen = binary.BigEndian.Uint16(data[2:4])
	hdr.ID = binary.BigEndian.Uint16(data[4:6])
	hdr.FlagsFrag = binary.BigEndian.Uint16(data[6:8])
	hdr.TTL = data[8]
	hdr.Protocol = data[9]
	hdr.Checksum = binary.BigEndian.Uint16(data[10:12])
	copy(hdr.SrcIP[:], data[12:16])
	copy(hdr.DstIP[:], data[16:20])

	end := int(hdr.TotalLen)
	if end > len(data) || end < ihl {
		end = len(data)
	}
	return hdr, data[ihl:end], true
}

// CalculateChecksum computes RFC 1071 internet checksum
func CalculateChecksum(data []byte) uint16 {
	var sum uint32
	for i := 0; i < len(data)-1; i += 2 {
		sum += uint32(binary.BigEndian.Uint16(data[i : i+2]))
	}
	if len(data)%2 == 1 {
		sum += uint32(data[len(data)-1]) << 8
	}
	for (sum >> 16) > 0 {
		sum = (sum & 0xFFFF) + (sum >> 16)
	}
	return ^uint16(sum)
}

// BuildIPv4 constructs an IPv4 packet with checksum
func BuildIPv4(srcIP, dstIP [4]byte, protocol uint8, payload []byte) []byte {
	totalLen := 20 + len(payload)
	out := make([]byte, totalLen)
	out[0] = 0x45 // IPv4, 5 words
	out[1] = 0x00
	binary.BigEndian.PutUint16(out[2:4], uint16(totalLen))
	binary.BigEndian.PutUint16(out[4:6], 0x1234) // ID
	binary.BigEndian.PutUint16(out[6:8], 0x4000) // Don't fragment
	out[8] = 64                                  // TTL
	out[9] = protocol
	copy(out[12:16], srcIP[:])
	copy(out[16:20], dstIP[:])

	csum := CalculateChecksum(out[:20])
	binary.BigEndian.PutUint16(out[10:12], csum)

	copy(out[20:], payload)
	return out
}

// BuildICMPEchoReply constructs an ICMP echo reply
func BuildICMPEchoReply(requestPayload []byte) []byte {
	if len(requestPayload) < 8 {
		return nil
	}
	reply := make([]byte, len(requestPayload))
	copy(reply, requestPayload)
	reply[0] = ICMPTypeEchoReply
	reply[1] = 0 // code
	reply[2] = 0 // clear checksum
	reply[3] = 0
	csum := CalculateChecksum(reply)
	binary.BigEndian.PutUint16(reply[2:4], csum)
	return reply
}

// IPv6Header represents 40-byte IPv6 header
type IPv6Header struct {
	VersionTrafficFlow uint32
	PayloadLen         uint16
	NextHeader         uint8
	HopLimit           uint8
	SrcIP              [16]byte
	DstIP              [16]byte
}

// ParseIPv6 parses IPv6 header and payload
func ParseIPv6(data []byte) (hdr IPv6Header, payload []byte, ok bool) {
	if len(data) < 40 {
		return hdr, nil, false
	}
	hdr.VersionTrafficFlow = binary.BigEndian.Uint32(data[0:4])
	hdr.PayloadLen = binary.BigEndian.Uint16(data[4:6])
	hdr.NextHeader = data[6]
	hdr.HopLimit = data[7]
	copy(hdr.SrcIP[:], data[8:24])
	copy(hdr.DstIP[:], data[24:40])

	end := 40 + int(hdr.PayloadLen)
	if end > len(data) {
		end = len(data)
	}
	return hdr, data[40:end], true
}

func IPTo4(ip net.IP) [4]byte {
	var out [4]byte
	ip4 := ip.To4()
	if ip4 != nil {
		copy(out[:], ip4)
	}
	return out
}

func IPTo16(ip net.IP) [16]byte {
	var out [16]byte
	ip6 := ip.To16()
	if ip6 != nil {
		copy(out[:], ip6)
	}
	return out
}
