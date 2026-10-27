#define _GNU_SOURCE
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
#include <time.h>
#include <fcntl.h>
#include "event_protocol.h"

static inline uint64_t get_time_ns(void) {
    struct timespec ts;
    clock_gettime(CLOCK_REALTIME, &ts);
    return ((uint64_t)ts.tv_sec * 1000000000ULL) + (uint64_t)ts.tv_nsec;
}

int main(int argc, char *argv[]) {
    if (argc < 2) {
        fprintf(stderr, "Usage: %s <file_to_modify> [num_iterations] [interval_ms]\n", argv[0]);
        return 1;
    }

    const char *target_file = argv[1];
    int iterations = (argc >= 3) ? atoi(argv[2]) : 50;
    int interval_ms = (argc >= 4) ? atoi(argv[3]) : 50;

    printf("[Host Latency Driver] Writing %d events to %s at %d ms intervals...\n",
           iterations, target_file, interval_ms);
    fflush(stdout);

    for (int i = 1; i <= iterations; i++) {
        char filepath[ORB_MAX_PATH_LEN];
        snprintf(filepath, sizeof(filepath), "%s_%d.txt", target_file, i);

        uint64_t t_host_write = get_time_ns();
        int fd = open(filepath, O_WRONLY | O_CREAT | O_TRUNC, 0644);
        if (fd >= 0) {
            char buf[64];
            int len = snprintf(buf, sizeof(buf), "DATA %d %llu\n", i, (unsigned long long)t_host_write);
            write(fd, buf, len);
            fsync(fd);
            close(fd);
        }

        printf("[HOST_WRITE #%d] file=%s ts_ns=%llu\n", i, filepath, (unsigned long long)t_host_write);
        fflush(stdout);

        usleep(interval_ms * 1000);
    }
    printf("[Host Latency Driver] Completed %d writes.\n", iterations);
    return 0;
}
