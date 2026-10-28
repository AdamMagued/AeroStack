//go:build darwin && cgo

package main

/*
#include <dns_sd.h>
#include <arpa/inet.h>
#include <stdlib.h>
#include <unistd.h>

static void DNSSD_API reg_reply_cb(
    DNSServiceRef sdRef,
    DNSRecordRef RecordRef,
    DNSServiceFlags flags,
    DNSServiceErrorType errorCode,
    void *context)
{
    // Registration callback from mDNSResponder
}

static DNSServiceRef create_mdns_conn() {
    DNSServiceRef sdRef = NULL;
    DNSServiceErrorType err = DNSServiceCreateConnection(&sdRef);
    if (err != kDNSServiceErr_NoError) {
        return NULL;
    }
    return sdRef;
}

static DNSRecordRef register_a_record(DNSServiceRef sdRef, const char *hostname, const char *ip_str) {
    struct in_addr addr;
    if (inet_pton(AF_INET, ip_str, &addr) <= 0) {
        return NULL;
    }

    DNSRecordRef recRef = NULL;
    DNSServiceErrorType err = DNSServiceRegisterRecord(
        sdRef,
        &recRef,
        kDNSServiceFlagsUnique,
        0, // all network interfaces
        hostname,
        kDNSServiceType_A,
        kDNSServiceClass_IN,
        sizeof(addr),
        &addr,
        120, // 120s TTL
        reg_reply_cb,
        NULL
    );
    if (err != kDNSServiceErr_NoError) {
        return NULL;
    }
    return recRef;
}

static DNSRecordRef register_aaaa_record(DNSServiceRef sdRef, const char *hostname, const char *ip6_str) {
    struct in6_addr addr6;
    if (inet_pton(AF_INET6, ip6_str, &addr6) <= 0) {
        return NULL;
    }

    DNSRecordRef recRef = NULL;
    DNSServiceErrorType err = DNSServiceRegisterRecord(
        sdRef,
        &recRef,
        kDNSServiceFlagsUnique,
        0,
        hostname,
        kDNSServiceType_AAAA,
        kDNSServiceClass_IN,
        sizeof(addr6),
        &addr6,
        120,
        reg_reply_cb,
        NULL
    );
    if (err != kDNSServiceErr_NoError) {
        return NULL;
    }
    return recRef;
}

static void process_mdns_events(DNSServiceRef sdRef, int *stopFlag) {
    int fd = DNSServiceRefSockFD(sdRef);
    fd_set readfds;
    while (*stopFlag == 0) {
        FD_ZERO(&readfds);
        FD_SET(fd, &readfds);
        struct timeval tv = { .tv_sec = 0, .tv_usec = 200000 };
        int res = select(fd + 1, &readfds, NULL, NULL, &tv);
        if (res > 0 && FD_ISSET(fd, &readfds)) {
            DNSServiceProcessResult(sdRef);
        }
    }
}
*/
import "C"
import (
	"fmt"
	"net"
	"strings"
	"sync"
	"unsafe"
)

// MDNSAdvertiser manages native Apple Bonjour registration on macOS
type MDNSAdvertiser struct {
	sdRef    C.DNSServiceRef
	stopFlag C.int
	records  map[string][]C.DNSRecordRef
	mu       sync.Mutex
}

// NewMDNSAdvertiser connects to macOS system mDNSResponder
func NewMDNSAdvertiser() (*MDNSAdvertiser, error) {
	ref := C.create_mdns_conn()
	if ref == nil {
		return nil, fmt.Errorf("failed to connect to mDNSResponder daemon")
	}

	adv := &MDNSAdvertiser{
		sdRef:   ref,
		records: make(map[string][]C.DNSRecordRef),
	}

	go adv.eventLoop()
	return adv, nil
}

func (a *MDNSAdvertiser) eventLoop() {
	C.process_mdns_events(a.sdRef, &a.stopFlag)
}

// RegisterHostname advertises a <container>.local or hostname via Bonjour
func (a *MDNSAdvertiser) RegisterHostname(name string, v4, v6 net.IP) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if !strings.HasSuffix(name, ".") {
		name = name + "."
	}

	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))

	var refs []C.DNSRecordRef

	if v4 != nil && v4.To4() != nil {
		cIP4 := C.CString(v4.To4().String())
		rec4 := C.register_a_record(a.sdRef, cName, cIP4)
		C.free(unsafe.Pointer(cIP4))
		if rec4 != nil {
			refs = append(refs, rec4)
		}
	}

	if v6 != nil && v6.To16() != nil {
		cIP6 := C.CString(v6.To16().String())
		rec6 := C.register_aaaa_record(a.sdRef, cName, cIP6)
		C.free(unsafe.Pointer(cIP6))
		if rec6 != nil {
			refs = append(refs, rec6)
		}
	}

	a.records[name] = refs
	return nil
}

// Close disconnects and deallocates records from mDNSResponder
func (a *MDNSAdvertiser) Close() {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.stopFlag = 1
	if a.sdRef != nil {
		C.DNSServiceRefDeallocate(a.sdRef)
		a.sdRef = nil
	}
}
