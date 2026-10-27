package main

import (
	"encoding/binary"
	"net"
	"testing"
)

func BenchmarkProcessEthernetARP(b *testing.B) {
	routerMAC := [6]byte{0x52, 0x54, 0x00, 0x12, 0x34, 0x56}
	routerIPv4 := [4]byte{192, 168, 128, 1}
	cfg := RouterConfig{
		RouterMAC:  routerMAC,
		RouterIPv4: routerIPv4,
	}
	router := NewUserspaceRouter(cfg, func([]byte) error { return nil })

	guestMAC := [6]byte{0x02, 0x42, 0xc0, 0xa8, 0x80, 0x0a}
	guestIPv4 := [4]byte{192, 168, 128, 10}

	arpReq := make([]byte, 28)
	binary.BigEndian.PutUint16(arpReq[0:2], 1)
	binary.BigEndian.PutUint16(arpReq[2:4], 0x0800)
	arpReq[4] = 6
	arpReq[5] = 4
	binary.BigEndian.PutUint16(arpReq[6:8], ARPOpRequest)
	copy(arpReq[8:14], guestMAC[:])
	copy(arpReq[14:18], guestIPv4[:])
	copy(arpReq[24:28], routerIPv4[:])

	bcastMAC := [6]byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}
	frame := BuildEthernet(bcastMAC, guestMAC, EtherTypeARP, arpReq)

	b.ResetTimer()
	b.SetBytes(int64(len(frame)))
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, _ = router.ProcessFrame(frame)
	}
}

func BenchmarkProcessICMPv4Echo(b *testing.B) {
	routerMAC := [6]byte{0x52, 0x54, 0x00, 0x12, 0x34, 0x56}
	routerIPv4 := [4]byte{192, 168, 128, 1}
	cfg := RouterConfig{
		RouterMAC:  routerMAC,
		RouterIPv4: routerIPv4,
	}
	router := NewUserspaceRouter(cfg, func([]byte) error { return nil })

	guestMAC := [6]byte{0x02, 0x42, 0xc0, 0xa8, 0x80, 0x0a}
	guestIPv4 := [4]byte{192, 168, 128, 10}

	icmpData := make([]byte, 56) // Standard 64-byte ping packet
	icmpEchoReq := make([]byte, 8+len(icmpData))
	icmpEchoReq[0] = ICMPTypeEchoRequest
	binary.BigEndian.PutUint16(icmpEchoReq[4:6], 0xABCD)
	binary.BigEndian.PutUint16(icmpEchoReq[6:8], 1)
	copy(icmpEchoReq[8:], icmpData)
	csum := CalculateChecksum(icmpEchoReq)
	binary.BigEndian.PutUint16(icmpEchoReq[2:4], csum)

	ipPkt := BuildIPv4(guestIPv4, routerIPv4, IPProtoICMP, icmpEchoReq)
	frame := BuildEthernet(routerMAC, guestMAC, EtherTypeIPv4, ipPkt)

	b.ResetTimer()
	b.SetBytes(int64(len(frame)))
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, _ = router.ProcessFrame(frame)
	}
}

func BenchmarkEmbeddedDNSQueryA(b *testing.B) {
	dnsServer := NewEmbeddedDNSServer("1.1.1.1:53")
	dnsServer.RegisterRecord("web.orb.local", net.ParseIP("192.168.128.10"), net.ParseIP("fd00:1234:5678::10"), 60)

	bind := "127.0.0.1:11053"
	if err := dnsServer.Start(bind); err != nil {
		b.Fatalf("Start failed: %v", err)
	}
	defer dnsServer.Stop()

	conn, err := net.Dial("udp", bind)
	if err != nil {
		b.Fatalf("Dial failed: %v", err)
	}
	defer conn.Close()

	req := make([]byte, 12)
	binary.BigEndian.PutUint16(req[0:2], 0x1122)
	binary.BigEndian.PutUint16(req[2:4], 0x0100)
	binary.BigEndian.PutUint16(req[4:6], 1)

	parts := []string{"web", "orb", "local"}
	for _, p := range parts {
		req = append(req, byte(len(p)))
		req = append(req, []byte(p)...)
	}
	req = append(req, 0)
	qmeta := make([]byte, 4)
	binary.BigEndian.PutUint16(qmeta[0:2], DNSTypeA)
	binary.BigEndian.PutUint16(qmeta[2:4], 1)
	req = append(req, qmeta...)

	resp := make([]byte, 512)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, _ = conn.Write(req)
		_, _ = conn.Read(resp)
	}
}

func BenchmarkInternetChecksum(b *testing.B) {
	data := make([]byte, 1500)
	for i := range data {
		data[i] = byte(i)
	}

	b.ResetTimer()
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_ = CalculateChecksum(data)
	}
}
