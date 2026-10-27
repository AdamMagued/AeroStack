package main

import (
	"encoding/binary"
	"net"
	"strings"
	"testing"
	"time"
)

func TestFrameParsingAndBuilding(t *testing.T) {
	destMAC := [6]byte{0x02, 0x00, 0x00, 0x00, 0x00, 0x01}
	srcMAC := [6]byte{0x02, 0x00, 0x00, 0x00, 0x00, 0x02}
	payload := []byte("orbstack_test_payload")

	ethFrame := BuildEthernet(destMAC, srcMAC, EtherTypeIPv4, payload)
	hdr, extractedPayload, ok := ParseEthernet(ethFrame)
	if !ok {
		t.Fatalf("Failed to parse ethernet frame")
	}
	if hdr.DestMAC != destMAC || hdr.SrcMAC != srcMAC || hdr.EtherType != EtherTypeIPv4 {
		t.Fatalf("Ethernet header mismatch: %+v", hdr)
	}
	if string(extractedPayload) != string(payload) {
		t.Fatalf("Payload mismatch: %s != %s", string(extractedPayload), string(payload))
	}
}

func TestUserspaceRouter_ARP(t *testing.T) {
	routerMAC := [6]byte{0x52, 0x54, 0x00, 0x12, 0x34, 0x56}
	routerIP := [4]byte{192, 168, 128, 1}
	guestMAC := [6]byte{0x02, 0x42, 0xc0, 0xa8, 0x80, 0x0a}
	guestIP := [4]byte{192, 168, 128, 10}

	cfg := RouterConfig{
		RouterMAC:  routerMAC,
		RouterIPv4: routerIP,
	}

	router := NewUserspaceRouter(cfg, nil)

	// Build ARP Request
	arpReq := make([]byte, 28)
	binary.BigEndian.PutUint16(arpReq[0:2], 1)
	binary.BigEndian.PutUint16(arpReq[2:4], 0x0800)
	arpReq[4] = 6
	arpReq[5] = 4
	binary.BigEndian.PutUint16(arpReq[6:8], ARPOpRequest)
	copy(arpReq[8:14], guestMAC[:])
	copy(arpReq[14:18], guestIP[:])
	copy(arpReq[24:28], routerIP[:])

	bcastMAC := [6]byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}
	ethFrame := BuildEthernet(bcastMAC, guestMAC, EtherTypeARP, arpReq)

	replyFrame, err := router.ProcessFrame(ethFrame)
	if err != nil {
		t.Fatalf("ProcessFrame failed: %v", err)
	}
	if replyFrame == nil {
		t.Fatalf("Expected ARP reply frame, got nil")
	}

	replyEth, replyPayload, ok := ParseEthernet(replyFrame)
	if !ok {
		t.Fatalf("Failed to parse ARP reply ethernet")
	}
	if replyEth.DestMAC != guestMAC {
		t.Fatalf("ARP reply DestMAC mismatch: %v != %v", replyEth.DestMAC, guestMAC)
	}

	replyARP, ok := ParseARP(replyPayload)
	if !ok {
		t.Fatalf("Failed to parse ARP payload")
	}
	if replyARP.OpCode != ARPOpReply {
		t.Fatalf("Expected ARPOpReply, got %d", replyARP.OpCode)
	}
	if replyARP.SenderMAC != routerMAC {
		t.Fatalf("Expected sender MAC to be router MAC, got %v", replyARP.SenderMAC)
	}

	stats := router.Stats()
	if stats.ArpHandled != 1 {
		t.Fatalf("Expected ArpHandled=1, got %d", stats.ArpHandled)
	}
}

func TestUserspaceRouter_ICMP(t *testing.T) {
	routerMAC := [6]byte{0x52, 0x54, 0x00, 0x12, 0x34, 0x56}
	routerIP := [4]byte{192, 168, 128, 1}
	guestMAC := [6]byte{0x02, 0x42, 0xc0, 0xa8, 0x80, 0x0a}
	guestIP := [4]byte{192, 168, 128, 10}

	cfg := RouterConfig{
		RouterMAC:  routerMAC,
		RouterIPv4: routerIP,
	}

	router := NewUserspaceRouter(cfg, nil)

	// Build ICMP Echo Request
	icmpPayload := []byte("ping_data_12345678")
	icmpReq := make([]byte, 8+len(icmpPayload))
	icmpReq[0] = ICMPTypeEchoRequest
	icmpReq[1] = 0
	binary.BigEndian.PutUint16(icmpReq[4:6], 0x1122) // ID
	binary.BigEndian.PutUint16(icmpReq[6:8], 1)      // Seq
	copy(icmpReq[8:], icmpPayload)
	csum := CalculateChecksum(icmpReq)
	binary.BigEndian.PutUint16(icmpReq[2:4], csum)

	ipv4Packet := BuildIPv4(guestIP, routerIP, IPProtoICMP, icmpReq)
	ethFrame := BuildEthernet(routerMAC, guestMAC, EtherTypeIPv4, ipv4Packet)

	replyFrame, err := router.ProcessFrame(ethFrame)
	if err != nil {
		t.Fatalf("ICMP ProcessFrame failed: %v", err)
	}
	if replyFrame == nil {
		t.Fatalf("Expected ICMP reply frame, got nil")
	}

	replyEth, replyIPPayload, ok := ParseEthernet(replyFrame)
	if !ok || replyEth.DestMAC != guestMAC {
		t.Fatalf("Invalid reply ethernet: %+v", replyEth)
	}

	replyIPHdr, replyICMPPayload, ok := ParseIPv4(replyIPPayload)
	if !ok || replyIPHdr.SrcIP != routerIP || replyIPHdr.DstIP != guestIP {
		t.Fatalf("Invalid reply IP header: %+v", replyIPHdr)
	}

	if len(replyICMPPayload) < 8 || replyICMPPayload[0] != ICMPTypeEchoReply {
		t.Fatalf("Invalid ICMP echo reply type: %d", replyICMPPayload[0])
	}
}

func TestEmbeddedDNS_OrbLocalResolution(t *testing.T) {
	dnsServer := NewEmbeddedDNSServer("1.1.1.1:53")
	defer dnsServer.Stop()

	targetIPv4 := net.ParseIP("192.168.128.99")
	targetIPv6 := net.ParseIP("fd00:1234:5678::99")
	dnsServer.RegisterRecord("web.myapp.orb.local", targetIPv4, targetIPv6, 300)

	// Bind to random UDP port on localhost
	udpAddr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Resolve error: %v", err)
	}
	conn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		t.Fatalf("Listen error: %v", err)
	}
	dnsServer.conn = conn
	dnsServer.running = 1
	go dnsServer.serve()

	serverAddr := conn.LocalAddr().String()

	// 1. Query A Record
	clientConn, err := net.Dial("udp", serverAddr)
	if err != nil {
		t.Fatalf("Failed to dial DNS: %v", err)
	}
	defer clientConn.Close()

	// Build DNS query packet for "web.myapp.orb.local" Type A
	query := buildDNSQuery("web.myapp.orb.local", DNSTypeA, 0x1234)
	if _, err := clientConn.Write(query); err != nil {
		t.Fatalf("Failed to write query: %v", err)
	}

	respBuf := make([]byte, 512)
	_ = clientConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, err := clientConn.Read(respBuf)
	if err != nil {
		t.Fatalf("Failed to read DNS reply: %v", err)
	}

	if n < 12 {
		t.Fatalf("DNS response too short: %d", n)
	}
	id := binary.BigEndian.Uint16(respBuf[0:2])
	if id != 0x1234 {
		t.Fatalf("DNS ID mismatch: 0x%04X", id)
	}
	anCount := binary.BigEndian.Uint16(respBuf[6:8])
	if anCount != 1 {
		t.Fatalf("Expected 1 answer, got %d", anCount)
	}

	// Read answer IP (last 4 bytes of A record)
	resolvedIP := net.IP(respBuf[n-4 : n])
	if !resolvedIP.Equal(targetIPv4) {
		t.Fatalf("Resolved IP %s != %s", resolvedIP, targetIPv4)
	}

	// 2. Query AAAA Record
	query6 := buildDNSQuery("web.myapp.orb.local", DNSTypeAAAA, 0x5678)
	if _, err := clientConn.Write(query6); err != nil {
		t.Fatalf("Failed to write query6: %v", err)
	}

	n6, err := clientConn.Read(respBuf)
	if err != nil {
		t.Fatalf("Failed to read DNS AAAA reply: %v", err)
	}
	anCount6 := binary.BigEndian.Uint16(respBuf[6:8])
	if anCount6 != 1 {
		t.Fatalf("Expected 1 AAAA answer, got %d", anCount6)
	}
	resolvedIP6 := net.IP(respBuf[n6-16 : n6])
	if !resolvedIP6.Equal(targetIPv6) {
		t.Fatalf("Resolved IPv6 %s != %s", resolvedIP6, targetIPv6)
	}
}

func buildDNSQuery(domain string, qtype uint16, id uint16) []byte {
	out := make([]byte, 12)
	binary.BigEndian.PutUint16(out[0:2], id)
	binary.BigEndian.PutUint16(out[2:4], 0x0100) // Standard query
	binary.BigEndian.PutUint16(out[4:6], 1)      // QDCOUNT = 1

	labels := splitDomain(domain)
	for _, l := range labels {
		out = append(out, byte(len(l)))
		out = append(out, []byte(l)...)
	}
	out = append(out, 0) // root null label

	typeClass := make([]byte, 4)
	binary.BigEndian.PutUint16(typeClass[0:2], qtype)
	binary.BigEndian.PutUint16(typeClass[2:4], 1) // Class IN
	out = append(out, typeClass...)
	return out
}

func splitDomain(d string) []string {
	var parts []string
	for _, p := range strings.Split(d, ".") {
		if p != "" {
			parts = append(parts, p)
		}
	}
	return parts
}

func TestMDNSAdvertiser(t *testing.T) {
	adv, err := NewMDNSAdvertiser()
	if err != nil {
		t.Logf("MDNSAdvertiser not available in this environment: %v", err)
		return
	}
	defer adv.Close()

	err = adv.RegisterHostname("testcontainer.local", net.ParseIP("192.168.128.20"), nil)
	if err != nil {
		t.Fatalf("RegisterHostname failed: %v", err)
	}
}
