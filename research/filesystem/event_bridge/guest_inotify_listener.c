#define _GNU_SOURCE
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
#include <sys/inotify.h>
#include <sys/time.h>
#include <time.h>
#include <fcntl.h>

#define EVENT_SIZE  (sizeof(struct inotify_event))
#define BUF_LEN     (1024 * (EVENT_SIZE + 16))

static inline uint64_t get_time_ns(void) {
    struct timespec ts;
    clock_gettime(CLOCK_REALTIME, &ts);
    return ((uint64_t)ts.tv_sec * 1000000000ULL) + (uint64_t)ts.tv_nsec;
}

int main(int argc, char *argv[]) {
    if (argc < 2) {
        fprintf(stderr, "Usage: %s <file_to_watch> [target_events]\n", argv[0]);
        return 1;
    }

    const char *target_file = argv[1];
    int target_count = (argc >= 3) ? atoi(argv[2]) : 50;

    int fd = inotify_init1(IN_CLOEXEC);
    if (fd < 0) {
        perror("inotify_init1");
        return 1;
    }

    int wd = inotify_add_watch(fd, target_file, IN_MODIFY | IN_ATTRIB | IN_CLOSE_WRITE);
    if (wd < 0) {
        perror("inotify_add_watch");
        return 1;
    }

    printf("[Guest Inotify Listener] Watching: %s (wd=%d)\n", target_file, wd);
    printf("[Guest Inotify Listener] Ready for %d events.\n", target_count);
    fflush(stdout);

    char buffer[BUF_LEN];
    int count = 0;

    while (count < target_count) {
        ssize_t length = read(fd, buffer, BUF_LEN);
        uint64_t recv_ns = get_time_ns();

        if (length < 0) {
            perror("read");
            break;
        }

        int i = 0;
        while (i < length) {
            struct inotify_event *event = (struct inotify_event *)&buffer[i];
            if (event->mask & (IN_MODIFY | IN_ATTRIB | IN_CLOSE_WRITE)) {
                count++;
                printf("[INOTIFY_EVENT #%d] mask=0x%08X recv_ts_ns=%lu\n",
                       count, event->mask, recv_ns);
                fflush(stdout);
            }
            i += EVENT_SIZE + event->len;
        }
    }

    printf("[Guest Inotify Listener] Successfully captured %d inotify events!\n", count);
    close(fd);
    return 0;
}
