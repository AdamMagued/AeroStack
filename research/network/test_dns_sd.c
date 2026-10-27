#include <stdio.h>
#include <stdlib.h>
#include <dns_sd.h>
#include <arpa/inet.h>
#include <unistd.h>

static void DNSSD_API reg_reply(
    DNSServiceRef sdRef,
    DNSRecordRef RecordRef,
    DNSServiceFlags flags,
    DNSServiceErrorType errorCode,
    void *context)
{
    printf("DNSServiceRegisterRecord callback errorCode: %d\n", errorCode);
}

int main() {
    DNSServiceRef sdRef = NULL;
    DNSServiceErrorType err = DNSServiceCreateConnection(&sdRef);
    if (err != kDNSServiceErr_NoError) {
        printf("DNSServiceCreateConnection failed: %d\n", err);
        return 1;
    }
    printf("Connected to mDNSResponder daemon (sdRef: %p)\n", sdRef);

    // Register test-prototype.local with IP 192.168.138.99
    struct in_addr addr;
    inet_pton(AF_INET, "192.168.138.99", &addr);

    DNSRecordRef recRef = NULL;
    err = DNSServiceRegisterRecord(
        sdRef,
        &recRef,
        kDNSServiceFlagsUnique, // flags: MUST be Unique or Shared
        0, // interfaceIndex (0 = all interfaces)
        "test-prototype.local.",
        kDNSServiceType_A,
        kDNSServiceClass_IN,
        sizeof(addr),
        &addr,
        120, // ttl
        reg_reply,
        NULL
    );

    if (err != kDNSServiceErr_NoError) {
        printf("DNSServiceRegisterRecord failed: %d\n", err);
        DNSServiceRefDeallocate(sdRef);
        return 1;
    }

    printf("Record registered successfully! Processing events for 5 seconds...\n");
    fd_set readfds;
    int dns_sd_fd = DNSServiceRefSockFD(sdRef);

    for (int i = 0; i < 5; i++) {
        FD_ZERO(&readfds);
        FD_SET(dns_sd_fd, &readfds);
        struct timeval tv = { .tv_sec = 1, .tv_usec = 0 };
        int res = select(dns_sd_fd + 1, &readfds, NULL, NULL, &tv);
        if (res > 0 && FD_ISSET(dns_sd_fd, &readfds)) {
            DNSServiceProcessResult(sdRef);
        }
    }

    DNSServiceRefDeallocate(sdRef);
    printf("Cleaned up mDNS record.\n");
    return 0;
}
