package main

import (
	"encoding/binary"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// DNS Query Types
const (
	DNSTypeA     = 1
	DNSTypeAAAA  = 28
	DNSTypeCNAME = 5
	DNSTypePTR   = 12
)

// RecordEntry holds IPv4 and IPv6 mappings for a hostname
type RecordEntry struct {
	IPv4 net.IP
	IPv6 net.IP
	TTL  uint32
}

// EmbeddedDNSServer handles dual-stack DNS queries for .orb.local
type EmbeddedDNSServer struct {
	records  map[string]RecordEntry
	mu       sync.RWMutex
	conn     *net.UDPConn
	upstream string
	queryCnt uint64
	running  int32
}

// NewEmbeddedDNSServer constructs a new DNS server instance
func NewEmbeddedDNSServer(upstream string) *EmbeddedDNSServer {
	if upstream == "" {
		upstream = "1.1.1.1:53"
	}
	return &EmbeddedDNSServer{
		records:  make(map[string]RecordEntry),
		upstream: upstream,
	}
}

// RegisterRecord adds or updates an A/AAAA DNS mapping
func (s *EmbeddedDNSServer) RegisterRecord(domain string, v4, v6 net.IP, ttl uint32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	domain = strings.ToLower(strings.TrimSuffix(domain, "."))
	if ttl == 0 {
		ttl = 60
	}
	s.records[domain] = RecordEntry{IPv4: v4, IPv6: v6, TTL: ttl}
}

// UnregisterRecord removes a domain record
func (s *EmbeddedDNSServer) UnregisterRecord(domain string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	domain = strings.ToLower(strings.TrimSuffix(domain, "."))
	delete(s.records, domain)
}

// Lookup queries the local cache for domain
func (s *EmbeddedDNSServer) Lookup(domain string) (RecordEntry, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	domain = strings.ToLower(strings.TrimSuffix(domain, "."))
	rec, ok := s.records[domain]
	return rec, ok
}

// Start begins listening on the specified UDP address (e.g. "127.0.0.1:53")
func (s *EmbeddedDNSServer) Start(bindAddr string) error {
	addr, err := net.ResolveUDPAddr("udp", bindAddr)
	if err != nil {
		return err
	}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		return err
	}
	s.conn = conn
	atomic.StoreInt32(&s.running, 1)

	go s.serve()
	return nil
}

// Stop terminates the DNS server
func (s *EmbeddedDNSServer) Stop() {
	atomic.StoreInt32(&s.running, 0)
	if s.conn != nil {
		_ = s.conn.Close()
	}
}

// QueryCount returns total queries processed
func (s *EmbeddedDNSServer) QueryCount() uint64 {
	return atomic.LoadUint64(&s.queryCnt)
}

func (s *EmbeddedDNSServer) serve() {
	buf := make([]byte, 2048)
	for atomic.LoadInt32(&s.running) == 1 {
		n, remoteAddr, err := s.conn.ReadFrom(buf)
		if err != nil {
			break
		}
		atomic.AddUint64(&s.queryCnt, 1)

		req := make([]byte, n)
		copy(req, buf[:n])
		go s.handleQuery(req, remoteAddr)
	}
}

func (s *EmbeddedDNSServer) handleQuery(req []byte, remoteAddr net.Addr) {
	if len(req) < 12 {
		return
	}
	id := binary.BigEndian.Uint16(req[0:2])

	domain, qtype, _, ok := parseQuestion(req[12:])
	if !ok {
		return
	}

	domainKey := strings.ToLower(domain)
	s.mu.RLock()
	record, exists := s.records[domainKey]
	s.mu.RUnlock()

	if exists {
		resp := s.buildResponse(id, req[12:], domain, qtype, record)
		if resp != nil {
			_, _ = s.conn.WriteTo(resp, remoteAddr)
		}
	} else if strings.HasSuffix(domainKey, ".orb.local") || strings.HasSuffix(domainKey, ".aerostack.local") {
		// Authoritative NXDOMAIN for unmapped .orb.local or .aerostack.local domains
		resp := s.buildNXDomainResponse(id, req[12:])
		if resp != nil {
			_, _ = s.conn.WriteTo(resp, remoteAddr)
		}
	} else {
		// Forward non-.orb.local to upstream resolver
		resp, err := s.forwardUpstream(req)
		if err == nil && resp != nil {
			_, _ = s.conn.WriteTo(resp, remoteAddr)
		}
	}
}

func parseQuestion(data []byte) (domain string, qtype uint16, qclass uint16, ok bool) {
	pos := 0
	var labels []string
	for pos < len(data) {
		lenByte := int(data[pos])
		if lenByte == 0 {
			pos++
			break
		}
		if pos+1+lenByte > len(data) {
			return "", 0, 0, false
		}
		labels = append(labels, string(data[pos+1:pos+1+lenByte]))
		pos += 1 + lenByte
	}
	if pos+4 > len(data) {
		return "", 0, 0, false
	}
	qtype = binary.BigEndian.Uint16(data[pos : pos+2])
	qclass = binary.BigEndian.Uint16(data[pos+2 : pos+4])
	return strings.Join(labels, "."), qtype, qclass, true
}

func (s *EmbeddedDNSServer) buildResponse(id uint16, questionBytes []byte, domain string, qtype uint16, rec RecordEntry) []byte {
	out := make([]byte, 12)
	binary.BigEndian.PutUint16(out[0:2], id)
	binary.BigEndian.PutUint16(out[2:4], 0x8180) // QR=1, AA=1, RD=1, RA=1
	binary.BigEndian.PutUint16(out[4:6], 1)      // QDCOUNT = 1

	qLen := 0
	for qLen < len(questionBytes) {
		l := int(questionBytes[qLen])
		if l == 0 {
			qLen += 5 // 0 null + 2 bytes type + 2 bytes class
			break
		}
		qLen += 1 + l
	}
	if qLen > len(questionBytes) {
		return nil
	}
	out = append(out, questionBytes[:qLen]...)

	var anCount uint16
	if qtype == DNSTypeA && rec.IPv4 != nil {
		ip4 := rec.IPv4.To4()
		if ip4 != nil {
			anCount = 1
			ans := make([]byte, 16)
			ans[0] = 0xC0
			ans[1] = 0x0C // pointer to question name
			binary.BigEndian.PutUint16(ans[2:4], DNSTypeA)
			binary.BigEndian.PutUint16(ans[4:6], 1) // Class IN
			binary.BigEndian.PutUint32(ans[6:10], rec.TTL)
			binary.BigEndian.PutUint16(ans[10:12], 4)
			copy(ans[12:16], ip4)
			out = append(out, ans...)
		}
	} else if qtype == DNSTypeAAAA && rec.IPv6 != nil {
		ip6 := rec.IPv6.To16()
		if ip6 != nil {
			anCount = 1
			ans := make([]byte, 28)
			ans[0] = 0xC0
			ans[1] = 0x0C
			binary.BigEndian.PutUint16(ans[2:4], DNSTypeAAAA)
			binary.BigEndian.PutUint16(ans[4:6], 1)
			binary.BigEndian.PutUint32(ans[6:10], rec.TTL)
			binary.BigEndian.PutUint16(ans[10:12], 16)
			copy(ans[12:28], ip6)
			out = append(out, ans...)
		}
	}

	binary.BigEndian.PutUint16(out[6:8], anCount)
	return out
}

func (s *EmbeddedDNSServer) buildNXDomainResponse(id uint16, questionBytes []byte) []byte {
	out := make([]byte, 12)
	binary.BigEndian.PutUint16(out[0:2], id)
	binary.BigEndian.PutUint16(out[2:4], 0x8183) // QR=1, AA=1, RCODE=3 (NXDOMAIN)
	binary.BigEndian.PutUint16(out[4:6], 1)      // QDCOUNT = 1

	qLen := 0
	for qLen < len(questionBytes) {
		l := int(questionBytes[qLen])
		if l == 0 {
			qLen += 5
			break
		}
		qLen += 1 + l
	}
	if qLen <= len(questionBytes) {
		out = append(out, questionBytes[:qLen]...)
	}
	return out
}

func (s *EmbeddedDNSServer) forwardUpstream(req []byte) ([]byte, error) {
	conn, err := net.DialTimeout("udp", s.upstream, 1500*time.Millisecond)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	if _, err := conn.Write(req); err != nil {
		return nil, err
	}

	resp := make([]byte, 2048)
	_ = conn.SetReadDeadline(time.Now().Add(1500 * time.Millisecond))
	n, err := conn.Read(resp)
	if err != nil {
		return nil, err
	}
	return resp[:n], nil
}
