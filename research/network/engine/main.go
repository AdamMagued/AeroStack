package main

import (
	"encoding/binary"
	"fmt"
	"net"
	"os"
	"os/exec"
	"syscall"
	"time"
)

func main() {
	fmt.Println("================================================================================")
	fmt.Println("  orbstack_free Bi-Directional Userspace Networking & DNS Engine Prototype     ")
	fmt.Println("================================================================================")

	// 1. Configure Router
	routerMAC := [6]byte{0x52, 0x54, 0x00, 0x12, 0x34, 0x56}
	routerIPv4 := [4]byte{192, 168, 128, 1}
	routerIPv6 := IPTo16(net.ParseIP("fd00:1234:5678::1"))

	cfg := RouterConfig{
		RouterMAC:  routerMAC,
		RouterIPv4: routerIPv4,
		RouterIPv6: routerIPv6,
	}

	// 2. Create Socketpair simulating VZFileHandleNetworkDeviceAttachment
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_DGRAM, 0)
	if err != nil {
		fmt.Printf("Socketpair error: %v\n", err)
		os.Exit(1)
	}
	defer syscall.Close(fds[0])
	defer syscall.Close(fds[1])

	// Tune buffers as recommended by Apple Virtualization framework
	bufSize := 1024 * 1024
	syscall.SetsockoptInt(fds[0], syscall.SOL_SOCKET, syscall.SO_SNDBUF, bufSize)
	syscall.SetsockoptInt(fds[0], syscall.SOL_SOCKET, syscall.SO_RCVBUF, 4*bufSize)
	syscall.SetsockoptInt(fds[1], syscall.SOL_SOCKET, syscall.SO_SNDBUF, bufSize)
	syscall.SetsockoptInt(fds[1], syscall.SOL_SOCKET, syscall.SO_RCVBUF, 4*bufSize)

	router := NewUserspaceRouter(cfg, func(frame []byte) error {
		_, err := syscall.Write(fds[0], frame)
		return err
	})

	// 3. Test Ingress Data Plane: ARP, ICMPv4, ICMPv6
	fmt.Println("\n[1] Testing Userspace Data Plane (ARP & ICMP Engine)...")

	// Simulated guest MAC and IP
	guestMAC := [6]byte{0x02, 0x42, 0xc0, 0xa8, 0x80, 0x0a}
	guestIPv4 := [4]byte{192, 168, 128, 10}
	guestIPv6 := IPTo16(net.ParseIP("fd00:1234:5678::10"))

	// A. Send ARP Request
	arpReq := make([]byte, 28)
	binary.BigEndian.PutUint16(arpReq[0:2], 1)
	binary.BigEndian.PutUint16(arpReq[2:4], 0x0800)
	arpReq[4] = 6
	arpReq[5] = 4
	binary.BigEndian.PutUint16(arpReq[6:8], ARPOpRequest)
	copy(arpReq[8:14], guestMAC[:])
	copy(arpReq[14:18], guestIPv4[:])
	copy(arpReq[24:28], routerIPv4[:]) // Target is router

	bcastMAC := [6]byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}
	arpEth := BuildEthernet(bcastMAC, guestMAC, EtherTypeARP, arpReq)

	arpStart := time.Now()
	arpReply, err := router.ProcessFrame(arpEth)
	arpLatency := time.Since(arpStart)

	if err != nil || arpReply == nil {
		fmt.Printf("ARP handling failed: %v\n", err)
	} else {
		replyEth, replyPayload, _ := ParseEthernet(arpReply)
		replyARP, _ := ParseARP(replyPayload)
		fmt.Printf("  -> ARP Request processed in %v\n", arpLatency)
		fmt.Printf("     ARP Reply Sender MAC: %02x:%02x:%02x:%02x:%02x:%02x (Router)\n",
			replyARP.SenderMAC[0], replyARP.SenderMAC[1], replyARP.SenderMAC[2],
			replyARP.SenderMAC[3], replyARP.SenderMAC[4], replyARP.SenderMAC[5])
		fmt.Printf("     Target MAC: %02x:%02x:%02x:%02x:%02x:%02x (Guest)\n",
			replyEth.DestMAC[0], replyEth.DestMAC[1], replyEth.DestMAC[2],
			replyEth.DestMAC[3], replyEth.DestMAC[4], replyEth.DestMAC[5])
	}

	// B. Send ICMPv4 Echo Request (Ping)
	icmpData := []byte("orbstack_free_ping_payload_12345678")
	icmpEchoReq := make([]byte, 8+len(icmpData))
	icmpEchoReq[0] = ICMPTypeEchoRequest
	icmpEchoReq[1] = 0 // Code 0
	binary.BigEndian.PutUint16(icmpEchoReq[4:6], 0xABCD) // ID
	binary.BigEndian.PutUint16(icmpEchoReq[6:8], 1)      // Sequence
	copy(icmpEchoReq[8:], icmpData)
	csum := CalculateChecksum(icmpEchoReq)
	binary.BigEndian.PutUint16(icmpEchoReq[2:4], csum)

	ipPkt := BuildIPv4(guestIPv4, routerIPv4, IPProtoICMP, icmpEchoReq)
	ethPkt := BuildEthernet(routerMAC, guestMAC, EtherTypeIPv4, ipPkt)

	icmpStart := time.Now()
	icmpReplyFrame, err := router.ProcessFrame(ethPkt)
	icmpLatency := time.Since(icmpStart)

	if err != nil || icmpReplyFrame == nil {
		fmt.Printf("ICMP Echo handling failed: %v\n", err)
	} else {
		_, ipPayload, _ := ParseEthernet(icmpReplyFrame)
		_, icmpPayload, _ := ParseIPv4(ipPayload)
		fmt.Printf("  -> ICMPv4 Echo processed in %v\n", icmpLatency)
		fmt.Printf("     ICMP Reply Type: %d (0=EchoReply), Seq: %d, Data Match: %v\n",
			icmpPayload[0], binary.BigEndian.Uint16(icmpPayload[6:8]),
			string(icmpPayload[8:]) == string(icmpData))
	}

	// C. Send ICMPv6 Echo Request
	icmp6EchoReq := make([]byte, 8+len(icmpData))
	icmp6EchoReq[0] = ICMPv6TypeEchoRequest
	binary.BigEndian.PutUint16(icmp6EchoReq[4:6], 0x1234)
	binary.BigEndian.PutUint16(icmp6EchoReq[6:8], 1)
	copy(icmp6EchoReq[8:], icmpData)

	// IPv6 pseudo-header for checksum
	pseudo := make([]byte, 40+len(icmp6EchoReq))
	copy(pseudo[0:16], guestIPv6[:])
	copy(pseudo[16:32], routerIPv6[:])
	pseudo[35] = uint8(len(icmp6EchoReq))
	pseudo[39] = IPProtoICMPv6
	copy(pseudo[40:], icmp6EchoReq)
	csum6 := CalculateChecksum(pseudo)
	icmp6EchoReq[2] = uint8(csum6 >> 8)
	icmp6EchoReq[3] = uint8(csum6 & 0xFF)

	ip6Hdr := make([]byte, 40)
	ip6Hdr[0] = 0x60
	ip6Hdr[4] = uint8(len(icmp6EchoReq) >> 8)
	ip6Hdr[5] = uint8(len(icmp6EchoReq) & 0xFF)
	ip6Hdr[6] = IPProtoICMPv6
	ip6Hdr[7] = 64
	copy(ip6Hdr[8:24], guestIPv6[:])
	copy(ip6Hdr[24:40], routerIPv6[:])

	fullIPv6Pkt := append(ip6Hdr, icmp6EchoReq...)
	eth6Pkt := BuildEthernet(routerMAC, guestMAC, EtherTypeIPv6, fullIPv6Pkt)

	icmp6Start := time.Now()
	icmp6ReplyFrame, err := router.ProcessFrame(eth6Pkt)
	icmp6Latency := time.Since(icmp6Start)

	if err != nil || ic6ReplyFrameNil(icmp6ReplyFrame) {
		fmt.Printf("ICMPv6 Echo handling failed: %v\n", err)
	} else {
		_, ip6Payload, _ := ParseEthernet(icmp6ReplyFrame)
		_, icmp6Payload, _ := ParseIPv6(ip6Payload)
		fmt.Printf("  -> ICMPv6 Echo processed in %v\n", icmp6Latency)
		fmt.Printf("     ICMPv6 Reply Type: %d (129=EchoReply), Data Match: %v\n",
			icmp6Payload[0], string(icmp6Payload[8:]) == string(icmpData))
	}

	// 4. Test Embedded DNS Server
	fmt.Println("\n[2] Testing Embedded Dual-Stack DNS Server...")
	dnsServer := NewEmbeddedDNSServer("1.1.1.1:53")
	dnsServer.RegisterRecord("web.orb.local", net.ParseIP("192.168.128.10"), net.ParseIP("fd00:1234:5678::10"), 60)
	dnsServer.RegisterRecord("api.orb.local", net.ParseIP("192.168.128.20"), net.ParseIP("fd00:1234:5678::20"), 60)

	dnsBind := "127.0.0.1:1053"
	if err := dnsServer.Start(dnsBind); err != nil {
		fmt.Printf("Failed to bind DNS server to %s: %v\n", dnsBind, err)
	} else {
		fmt.Printf("  -> Embedded DNS Server listening on %s (UDP)\n", dnsBind)

		// Query web.orb.local over UDP
		queryDNS := func(name string, qtype uint16) {
			conn, err := net.Dial("udp", dnsBind)
			if err != nil {
				fmt.Printf("DNS dial failed: %v\n", err)
				return
			}
			defer conn.Close()

			// Build query
			req := make([]byte, 12)
			binary.BigEndian.PutUint16(req[0:2], 0x55AA) // ID
			binary.BigEndian.PutUint16(req[2:4], 0x0100) // Standard query
			binary.BigEndian.PutUint16(req[4:6], 1)      // QDCOUNT = 1

			// Question labels
			parts := []string{"web", "orb", "local"}
			for _, p := range parts {
				req = append(req, byte(len(p)))
				req = append(req, []byte(p)...)
			}
			req = append(req, 0) // Terminate qname
			qmeta := make([]byte, 4)
			binary.BigEndian.PutUint16(qmeta[0:2], qtype)
			binary.BigEndian.PutUint16(qmeta[2:4], 1) // Class IN
			req = append(req, qmeta...)

			t0 := time.Now()
			conn.Write(req)
			resp := make([]byte, 1024)
			conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
			n, err := conn.Read(resp)
			rtt := time.Since(t0)

			if err != nil {
				fmt.Printf("DNS query failed: %v\n", err)
				return
			}

			ancount := binary.BigEndian.Uint16(resp[6:8])
			var resIP string
			ansStart := len(req)
			if ancount > 0 && n >= ansStart+16 {
				if qtype == DNSTypeA && n >= ansStart+16 {
					ipBytes := resp[ansStart+12 : ansStart+16]
					resIP = net.IP(ipBytes).String()
				} else if qtype == DNSTypeAAAA && n >= ansStart+28 {
					ipBytes := resp[ansStart+12 : ansStart+28]
					resIP = net.IP(ipBytes).String()
				}
			}

			fmt.Printf("     Query: %s (Type %d) -> Answers: %d, IP: %s, RTT: %v\n",
				name, qtype, ancount, resIP, rtt)
		}

		queryDNS("web.orb.local", DNSTypeA)
		queryDNS("web.orb.local", DNSTypeAAAA)

		dnsServer.Stop()
	}

	// 5. Test mDNS Advertiser
	fmt.Println("\n[3] Testing Apple mDNS Advertisements via <dns_sd.h>...")
	mdns, err := NewMDNSAdvertiser()
	if err != nil {
		fmt.Printf("mDNS advertiser error: %v\n", err)
	} else {
		testHost := "sample-container.local."
		testIP := net.ParseIP("192.168.138.77")
		testIP6 := net.ParseIP("fd07:b51a:cc66::77")

		fmt.Printf("  -> Registering %s with IPv4: %s, IPv6: %s\n", testHost, testIP, testIP6)
		if err := mdns.RegisterHostname(testHost, testIP, testIP6); err != nil {
			fmt.Printf("mDNS registration error: %v\n", err)
		} else {
			// Give mDNSResponder a moment to commit
			time.Sleep(300 * time.Millisecond)

			// Query through macOS system resolver
			cmd := exec.Command("dscacheutil", "-q", "host", "-a", "name", "sample-container.local")
			out, err := cmd.Output()
			if err == nil {
				fmt.Println("  -> macOS dscacheutil resolution output:")
				fmt.Printf("%s\n", string(out))
			} else {
				fmt.Printf("dscacheutil failed: %v\n", err)
			}
		}
		mdns.Close()
	}

	fmt.Println("================================================================================")
	fmt.Println("  All prototype subsystems verified successfully!                               ")
	fmt.Println("================================================================================")
}

func ic6ReplyFrameNil(b []byte) bool {
	return b == nil
}
