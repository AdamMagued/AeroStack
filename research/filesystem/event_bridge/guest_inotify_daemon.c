#define _GNU_SOURCE
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
#include <time.h>
#include <sys/socket.h>
#include <netinet/in.h>
#include <netinet/tcp.h>
#include <sys/un.h>
#include <sys/stat.h>
#include <fcntl.h>
#include <errno.h>
#include <libgen.h>
#include "event_protocol.h"

#define MAX_CLIENTS 64

static char g_mount_prefix[ORB_MAX_PATH_LEN];
static size_t g_mount_prefix_len = 0;
static int g_ipc_sock = -1;
static int g_ipc_clients[MAX_CLIENTS];
static int g_num_ipc_clients = 0;

/* Latency statistics */
static uint64_t g_total_events = 0;
static double g_total_latency_us = 0.0;
static double g_min_latency_us = 999999999.0;
static double g_max_latency_us = 0.0;

static inline uint64_t get_guest_time_ns(void) {
    struct timespec ts;
    clock_gettime(CLOCK_REALTIME, &ts);
    return ((uint64_t)ts.tv_sec * 1000000000ULL) + (uint64_t)ts.tv_nsec;
}

static void trigger_kernel_vfs_event(const char *full_path, uint32_t mask) {
    struct stat st;
    int stat_res = stat(full_path, &st);

    if (mask & ORB_IN_MODIFY) {
        if (stat_res == 0 && S_ISREG(st.st_mode)) {
            /* Atomic non-destructive byte-0 rewrite */
            int fd = open(full_path, O_RDWR);
            if (fd >= 0) {
                char byte = 0;
                ssize_t r = pread(fd, &byte, 1, 0);
                if (r > 0) {
                    pwrite(fd, &byte, 1, 0);
                }
                close(fd); /* Emits IN_CLOSE_WRITE */
            } else if (errno == EACCES || errno == EROFS) {
                /* Fallback to utimensat if read-only */
                utimensat(AT_FDCWD, full_path, NULL, 0);
            }
        } else if (stat_res == 0 && S_ISDIR(st.st_mode)) {
            utimensat(AT_FDCWD, full_path, NULL, 0);
        }
    }

    if (mask & ORB_IN_ATTRIB) {
        if (stat_res == 0) {
            utimensat(AT_FDCWD, full_path, NULL, 0);
        }
    }

    if (mask & (ORB_IN_CREATE | ORB_IN_DELETE | ORB_IN_MOVE_SELF)) {
        /* Touch parent directory to notify directory watchers */
        char path_copy[ORB_MAX_PATH_LEN];
        strncpy(path_copy, full_path, sizeof(path_copy) - 1);
        path_copy[sizeof(path_copy) - 1] = '\0';
        char *parent = dirname(path_copy);
        if (parent && strlen(parent) > 0) {
            utimensat(AT_FDCWD, parent, NULL, 0);
        }
    }
}

static void broadcast_ipc_event(const char *rel_path, uint32_t mask, uint64_t timestamp_ns) {
    if (g_num_ipc_clients == 0) return;

    orb_event_header_t hdr;
    hdr.magic = ORB_FS_MAGIC;
    hdr.version = ORB_FS_VERSION;
    hdr.opcode = ORB_OP_EVENT;
    hdr.inotify_mask = mask;
    hdr.seq = g_total_events;
    hdr.timestamp_ns = timestamp_ns;
    hdr.path_len = (uint32_t)strlen(rel_path);

    for (int i = 0; i < g_num_ipc_clients; i++) {
        int client_fd = g_ipc_clients[i];
        if (client_fd < 0) continue;

        struct iovec iov[2];
        iov[0].iov_base = &hdr;
        iov[0].iov_len = sizeof(hdr);
        iov[1].iov_base = (void *)rel_path;
        iov[1].iov_len = hdr.path_len;

        struct msghdr msg;
        memset(&msg, 0, sizeof(msg));
        msg.msg_iov = iov;
        msg.msg_iovlen = 2;

        ssize_t s = sendmsg(client_fd, &msg, MSG_NOSIGNAL);
        if (s < 0) {
            close(client_fd);
            g_ipc_clients[i] = -1;
        }
    }
}

static int read_exact(int fd, void *buf, size_t count) {
    size_t total = 0;
    char *p = (char *)buf;
    while (total < count) {
        ssize_t r = read(fd, p + total, count - total);
        if (r <= 0) {
            return (r == 0) ? 0 : -1;
        }
        total += r;
    }
    return 1;
}

int main(int argc, char *argv[]) {
    if (argc < 3) {
        fprintf(stderr, "Usage: %s <guest_mount_prefix> <listen_port>\n", argv[0]);
        return 1;
    }

    const char *prefix = argv[1];
    int port = atoi(argv[2]);

    strncpy(g_mount_prefix, prefix, sizeof(g_mount_prefix) - 1);
    g_mount_prefix_len = strlen(g_mount_prefix);
    while (g_mount_prefix_len > 1 && g_mount_prefix[g_mount_prefix_len - 1] == '/') {
        g_mount_prefix[--g_mount_prefix_len] = '\0';
    }

    printf("[Guest Inotify Daemon] Mount prefix: %s\n", g_mount_prefix);
    printf("[Guest Inotify Daemon] Binding listen socket on 0.0.0.0:%d...\n", port);

    int listen_fd = socket(AF_INET, SOCK_STREAM, 0);
    if (listen_fd < 0) {
        perror("socket");
        return 1;
    }

    int opt = 1;
    setsockopt(listen_fd, SOL_SOCKET, SO_REUSEADDR, &opt, sizeof(opt));
    setsockopt(listen_fd, IPPROTO_TCP, TCP_NODELAY, &opt, sizeof(opt));

    struct sockaddr_in serv_addr;
    memset(&serv_addr, 0, sizeof(serv_addr));
    serv_addr.sin_family = AF_INET;
    serv_addr.sin_port = htons(port);
    serv_addr.sin_addr.s_addr = INADDR_ANY;

    if (bind(listen_fd, (struct sockaddr *)&serv_addr, sizeof(serv_addr)) < 0) {
        perror("bind");
        return 1;
    }

    if (listen(listen_fd, 5) < 0) {
        perror("listen");
        return 1;
    }

    printf("[Guest Inotify Daemon] Listening for host bridge connection on port %d...\n", port);
    fflush(stdout);

    struct sockaddr_in client_addr;
    socklen_t client_len = sizeof(client_addr);
    int client_fd = accept(listen_fd, (struct sockaddr *)&client_addr, &client_len);
    if (client_fd < 0) {
        perror("accept");
        return 1;
    }
    setsockopt(client_fd, IPPROTO_TCP, TCP_NODELAY, &opt, sizeof(opt));
    printf("[Guest Inotify Daemon] Host bridge connected!\n");
    fflush(stdout);

    char path_buf[ORB_MAX_PATH_LEN];
    char full_path[ORB_MAX_PATH_LEN * 2];

    while (1) {
        orb_event_header_t hdr;
        int r = read_exact(client_fd, &hdr, sizeof(hdr));
        if (r <= 0) {
            printf("[Guest Inotify Daemon] Host connection closed.\n");
            break;
        }

        if (hdr.magic != ORB_FS_MAGIC) {
            fprintf(stderr, "[Guest Inotify Daemon] Invalid magic: 0x%08X\n", hdr.magic);
            break;
        }

        if (hdr.path_len >= sizeof(path_buf)) {
            fprintf(stderr, "[Guest Inotify Daemon] Path len too large: %u\n", hdr.path_len);
            break;
        }

        r = read_exact(client_fd, path_buf, hdr.path_len);
        if (r <= 0) break;
        path_buf[hdr.path_len] = '\0';

        uint64_t guest_now_ns = get_guest_time_ns();
        double latency_us = (double)(guest_now_ns - hdr.timestamp_ns) / 1000.0;

        /* Track latency stats */
        if (latency_us > 0.0 && latency_us < 10000000.0) { /* Filter reasonable range */
            g_total_events++;
            g_total_latency_us += latency_us;
            if (latency_us < g_min_latency_us) g_min_latency_us = latency_us;
            if (latency_us > g_max_latency_us) g_max_latency_us = latency_us;
        }

        /* Build full path inside guest */
        snprintf(full_path, sizeof(full_path), "%s/%s", g_mount_prefix, path_buf);

        /* Trigger VFS inotify event */
        trigger_kernel_vfs_event(full_path, hdr.inotify_mask);

        /* Broadcast to userspace shim listeners */
        broadcast_ipc_event(path_buf, hdr.inotify_mask, hdr.timestamp_ns);

        /* Send ACK back to host bridge for precision RTT latency measurement */
        orb_event_header_t ack;
        ack.magic = ORB_FS_MAGIC;
        ack.version = ORB_FS_VERSION;
        ack.opcode = ORB_OP_ACK;
        ack.inotify_mask = hdr.inotify_mask;
        ack.seq = hdr.seq;
        ack.timestamp_ns = hdr.timestamp_ns;
        ack.path_len = 0;
        write(client_fd, &ack, sizeof(ack));

        printf("[EVENT #%lu] mask=0x%08X path='%s' processed -> ACK sent\n",
               g_total_events, hdr.inotify_mask, path_buf);
        fflush(stdout);
    }

    if (g_total_events > 0) {
        printf("\n================ INOTIFY LATENCY SUMMARY ================\n");
        printf("Total Events:  %lu\n", g_total_events);
        printf("Min Latency:   %.2f us (%.3f ms)\n", g_min_latency_us, g_min_latency_us / 1000.0);
        printf("Avg Latency:   %.2f us (%.3f ms)\n", g_total_latency_us / g_total_events, (g_total_latency_us / g_total_events) / 1000.0);
        printf("Max Latency:   %.2f us (%.3f ms)\n", g_max_latency_us, g_max_latency_us / 1000.0);
        printf("=========================================================\n");
    }

    close(client_fd);
    close(listen_fd);
    return 0;
}
