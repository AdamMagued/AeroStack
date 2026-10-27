#define _GNU_SOURCE
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
#include <fcntl.h>
#include <errno.h>
#include <poll.h>
#include <sys/types.h>
#include <sys/socket.h>
#include <netinet/in.h>
#include "vsock_mem_protocol.h"

// Fallback for macOS testing or systems without vsock.h
#if defined(__linux__)
#include <sys/socket.h>
#include <linux/vm_sockets.h>
#else
#define VMADDR_CID_HOST 2
struct sockaddr_vm {
    sa_family_t svm_family;
    unsigned short svm_reserved1;
    unsigned int svm_port;
    unsigned int svm_cid;
    unsigned char svm_zero[sizeof(struct sockaddr) -
                           sizeof(sa_family_t) -
                           sizeof(unsigned short) -
                           sizeof(unsigned int) -
                           sizeof(unsigned int)];
};
#endif

typedef struct {
    uint64_t total_bytes;
    uint64_t free_bytes;
    uint64_t avail_bytes;
    uint64_t cached_bytes;
    uint64_t dirty_bytes;
} MemInfoStats;

static int parse_meminfo(MemInfoStats *stats) {
    memset(stats, 0, sizeof(*stats));
    FILE *f = fopen("/proc/meminfo", "r");
    if (!f) return -1;

    char line[256];
    while (fgets(line, sizeof(line), f)) {
        uint64_t val;
        if (sscanf(line, "MemTotal: %llu kB", &val) == 1) {
            stats->total_bytes = val * 1024;
        } else if (sscanf(line, "MemFree: %llu kB", &val) == 1) {
            stats->free_bytes = val * 1024;
        } else if (sscanf(line, "MemAvailable: %llu kB", &val) == 1) {
            stats->avail_bytes = val * 1024;
        } else if (sscanf(line, "Cached: %llu kB", &val) == 1) {
            stats->cached_bytes = val * 1024;
        } else if (sscanf(line, "Dirty: %llu kB", &val) == 1) {
            stats->dirty_bytes = val * 1024;
        }
    }
    fclose(f);
    return 0;
}

static int parse_psi(uint32_t *psi_some, uint32_t *psi_full) {
    *psi_some = 0;
    *psi_full = 0;
    FILE *f = fopen("/proc/pressure/memory", "r");
    if (!f) return -1;

    char line[256];
    while (fgets(line, sizeof(line), f)) {
        double avg10, avg60, avg300;
        if (sscanf(line, "some avg10=%lf avg60=%lf avg300=%lf", &avg10, &avg60, &avg300) == 3) {
            *psi_some = (uint32_t)(avg10 * 100.0);
        } else if (sscanf(line, "full avg10=%lf avg60=%lf avg300=%lf", &avg10, &avg60, &avg300) == 3) {
            *psi_full = (uint32_t)(avg10 * 100.0);
        }
    }
    fclose(f);
    return 0;
}

__attribute__((unused)) static void trigger_compaction(void) {
    int fd = open("/proc/sys/vm/compact_memory", O_WRONLY);
    if (fd >= 0) {
        write(fd, "1\n", 2);
        close(fd);
    }
}

__attribute__((unused)) static void trigger_drop_caches(int mode) {
    int fd = open("/proc/sys/vm/drop_caches", O_WRONLY);
    if (fd >= 0) {
        char buf[8];
        snprintf(buf, sizeof(buf), "%d\n", mode);
        write(fd, buf, strlen(buf));
        close(fd);
    }
}

// Micro-benchmark and test runner for Guest Memory Agent
int main(int argc, char **argv) {
    printf("================================================================================\n");
    printf("Guest Memory Telemetry & Reclamation Agent (orb-memd)\n");
    printf("================================================================================\n");

    MemInfoStats mem = {0};
    uint32_t psi_some = 0, psi_full = 0;

    if (parse_meminfo(&mem) == 0) {
        printf("[OK] Live /proc/meminfo read:\n");
        printf("     Total: %llu MB | Free: %llu MB | Available: %llu MB\n",
               mem.total_bytes / (1024 * 1024),
               mem.free_bytes / (1024 * 1024),
               mem.avail_bytes / (1024 * 1024));
    } else {
        printf("[INFO] /proc/meminfo not accessible on macOS host. Simulating guest readings:\n");
        mem.total_bytes = 4096ULL * 1024 * 1024;
        mem.free_bytes  = 3800ULL * 1024 * 1024;
        mem.avail_bytes = 3900ULL * 1024 * 1024;
        printf("     [Simulated] Total: 4096 MB | Free: 3800 MB | Available: 3900 MB\n");
    }

    if (parse_psi(&psi_some, &psi_full) == 0) {
        printf("[OK] Live PSI pressure read: some_avg10=%.2f%%, full_avg10=%.2f%%\n",
               psi_some / 100.0, psi_full / 100.0);
    } else {
        printf("[INFO] PSI not present on macOS host (simulating 0.00%% stall).\n");
    }

    GuestTelemetryFrame frame = {
        .magic = PROTOCOL_MAGIC,
        .msg_type = MSG_TELEMETRY_REPORT,
        .total_ram_bytes = mem.total_bytes,
        .free_ram_bytes = mem.free_bytes,
        .avail_ram_bytes = mem.avail_bytes,
        .psi_some_avg10 = psi_some,
        .psi_full_avg10 = psi_full
    };

    printf("\nTelemetry frame serialized successfully (%zu bytes):\n", sizeof(frame));
    printf("  Magic: 0x%08X, Type: %u, Total: %llu MB, Avail: %llu MB\n",
           frame.magic, frame.msg_type,
           frame.total_ram_bytes / (1024 * 1024),
           frame.avail_ram_bytes / (1024 * 1024));

    return 0;
}
