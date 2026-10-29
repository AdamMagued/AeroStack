package main

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
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

func TestPortForwarder_TCP(t *testing.T) {
	// 1. Mock backend server (simulating container)
	backendListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to listen backend: %v", err)
	}
	defer backendListener.Close()

	backendPort := backendListener.Addr().(*net.TCPAddr).Port

	go func() {
		conn, err := backendListener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		buf := make([]byte, 1024)
		n, _ := conn.Read(buf)
		if n > 0 {
			_, _ = conn.Write(append([]byte("ECHO:"), buf[:n]...))
		}
	}()

	// 2. Port Forwarder
	pf := NewPortForwarder()
	pf.Start()
	defer pf.Stop()

	// Find free host port
	freeL, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to find free port: %v", err)
	}
	hostPort := freeL.Addr().(*net.TCPAddr).Port
	freeL.Close()

	if err := pf.ForwardTCP("127.0.0.1", hostPort, "127.0.0.1", backendPort); err != nil {
		t.Fatalf("ForwardTCP failed: %v", err)
	}

	// 3. Connect client to hostPort
	clientConn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", hostPort), 2*time.Second)
	if err != nil {
		t.Fatalf("Client dial failed: %v", err)
	}
	defer clientConn.Close()

	msg := "aerostack_stream_test"
	if _, err := clientConn.Write([]byte(msg)); err != nil {
		t.Fatalf("Client write failed: %v", err)
	}

	resp := make([]byte, 1024)
	n, err := clientConn.Read(resp)
	if err != nil {
		t.Fatalf("Client read failed: %v", err)
	}
	expected := "ECHO:" + msg
	if string(resp[:n]) != expected {
		t.Fatalf("Mismatch: expected %q, got %q", expected, string(resp[:n]))
	}

	// 4. Test RemoveForward
	if err := pf.RemoveForward("tcp", "127.0.0.1", hostPort); err != nil {
		t.Fatalf("RemoveForward failed: %v", err)
	}
}

func TestPortForwarder_TCP_HalfClose(t *testing.T) {
	backendListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to listen backend: %v", err)
	}
	defer backendListener.Close()

	backendPort := backendListener.Addr().(*net.TCPAddr).Port

	backendDone := make(chan struct{})
	go func() {
		defer close(backendDone)
		conn, err := backendListener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		data, err := io.ReadAll(conn)
		if err != nil {
			return
		}
		_, _ = conn.Write(append([]byte("HALF_CLOSE_REPLY:"), data...))
	}()

	pf := NewPortForwarder()
	pf.Start()
	defer pf.Stop()

	freeL, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to find free port: %v", err)
	}
	hostPort := freeL.Addr().(*net.TCPAddr).Port
	freeL.Close()

	if err := pf.ForwardTCP("127.0.0.1", hostPort, "127.0.0.1", backendPort); err != nil {
		t.Fatalf("ForwardTCP failed: %v", err)
	}
	defer func() {
		_ = pf.RemoveForward("tcp", "127.0.0.1", hostPort)
	}()

	clientConn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", hostPort))
	if err != nil {
		t.Fatalf("Client dial failed: %v", err)
	}
	defer clientConn.Close()

	payload := "streaming_request_payload"
	if _, err := clientConn.Write([]byte(payload)); err != nil {
		t.Fatalf("Client write failed: %v", err)
	}

	tcpClient, ok := clientConn.(*net.TCPConn)
	if !ok {
		t.Fatalf("Expected *net.TCPConn")
	}
	if err := tcpClient.CloseWrite(); err != nil {
		t.Fatalf("CloseWrite failed: %v", err)
	}

	resp, err := io.ReadAll(clientConn)
	if err != nil {
		t.Fatalf("Failed to read server response after half-close: %v", err)
	}

	expected := "HALF_CLOSE_REPLY:" + payload
	if string(resp) != expected {
		t.Fatalf("Mismatch: expected %q, got %q", expected, string(resp))
	}

	<-backendDone
}

func TestDNSServer_AerostackLocal(t *testing.T) {
	dns := NewEmbeddedDNSServer("1.1.1.1:53")
	dns.RegisterRecord("testbox.aerostack.local", net.ParseIP("192.168.128.77"), nil, 60)

	rec, ok := dns.Lookup("testbox.aerostack.local")
	if !ok {
		t.Fatalf("Expected testbox.aerostack.local to resolve")
	}
	if rec.IPv4.String() != "192.168.128.77" {
		t.Fatalf("Expected IP 192.168.128.77, got %s", rec.IPv4.String())
	}

	dns.UnregisterRecord("testbox.aerostack.local")
	_, ok = dns.Lookup("testbox.aerostack.local")
	if ok {
		t.Fatalf("Expected record to be removed after UnregisterRecord")
	}
}

func TestDockerPortWatcher_Lifecycle(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "docker_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	mockSock := filepath.Join(tmpDir, "docker.sock")
	l, err := net.Listen("unix", mockSock)
	if err != nil {
		t.Fatalf("Failed to listen on unix socket: %v", err)
	}
	defer l.Close()

	// Mock Docker server
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				r := bufio.NewReader(c)
				req, err := http.ReadRequest(r)
				if err != nil {
					return
				}

				if strings.Contains(req.URL.Path, "/containers/json") {
					resp := "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: 2\r\n\r\n[]"
					_, _ = c.Write([]byte(resp))
				} else if strings.Contains(req.URL.Path, "/events") {
					respHeader := "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nTransfer-Encoding: chunked\r\n\r\n"
					_, _ = c.Write([]byte(respHeader))

					// Emit start event
					startEv := `{"Type":"container","Action":"start","Actor":{"ID":"c123456789012"}}` + "\n"
					_, _ = fmt.Fprintf(c, "%x\r\n%s\r\n", len(startEv), startEv)

					// Wait for inspect
					<-time.After(100 * time.Millisecond)

					// Emit stop event
					stopEv := `{"Type":"container","Action":"stop","Actor":{"ID":"c123456789012"}}` + "\n"
					_, _ = fmt.Fprintf(c, "%x\r\n%s\r\n", len(stopEv), stopEv)
				} else if strings.Contains(req.URL.Path, "/containers/c123456789012/json") {
					inspectJSON := `{
						"Id": "c123456789012",
						"NetworkSettings": {
							"IPAddress": "172.17.0.2",
							"Ports": {
								"80/tcp": [{"HostIp": "127.0.0.1", "HostPort": "19080"}]
							}
						}
					}`
					resp := fmt.Sprintf("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: %d\r\n\r\n%s", len(inspectJSON), inspectJSON)
					_, _ = c.Write([]byte(resp))
				}
			}(conn)
		}
	}()

	pf := NewPortForwarder()
	pf.Start()
	defer pf.Stop()

	watcher := NewDockerPortWatcher(mockSock, pf, "192.168.128.2")
	watcher.Start()

	// Wait up to 2 seconds for event processing
	deadline := time.Now().Add(2 * time.Second)
	sawRule := false
	for time.Now().Before(deadline) {
		rules := pf.ListRules()
		for _, r := range rules {
			if r.HostPort == 19080 {
				sawRule = true
				break
			}
		}
		if sawRule {
			break
		}
		<-time.After(20 * time.Millisecond)
	}

	if !sawRule {
		t.Fatalf("Watcher did not forward port 19080 for started container")
	}

	// Wait for unforwarding after stop event
	deadline = time.Now().Add(2 * time.Second)
	unforwarded := false
	for time.Now().Before(deadline) {
		rules := pf.ListRules()
		hasRule := false
		for _, r := range rules {
			if r.HostPort == 19080 {
				hasRule = true
				break
			}
		}
		if !hasRule {
			unforwarded = true
			break
		}
		<-time.After(20 * time.Millisecond)
	}

	if !unforwarded {
		t.Fatalf("Watcher did not unforward port 19080 on container stop")
	}

	watcher.Stop()
}
