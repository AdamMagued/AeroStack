#define _GNU_SOURCE
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
#include <time.h>
#include <sys/socket.h>
#include <netinet/in.h>
#include <netinet/tcp.h>
#include <arpa/inet.h>
#include <fcntl.h>
#include <CoreServices/CoreServices.h>
#include <dispatch/dispatch.h>
#include "event_protocol.h"

static int g_sock_fd = -1;
static char g_watch_path[ORB_MAX_PATH_LEN];
static size_t g_watch_path_len = 0;
static uint64_t g_event_seq = 0;

static inline uint64_t get_host_time_ns(void) {
    struct timespec ts;
    clock_gettime(CLOCK_REALTIME, &ts);
    return ((uint64_t)ts.tv_sec * 1000000000ULL) + (uint64_t)ts.tv_nsec;
}

static uint32_t map_fsevents_to_inotify(FSEventStreamEventFlags flags) {
    uint32_t mask = 0;

    if (flags & kFSEventStreamEventFlagItemCreated) {
        mask |= ORB_IN_CREATE;
    }
    if (flags & kFSEventStreamEventFlagItemRemoved) {
        mask |= ORB_IN_DELETE;
    }
    if (flags & kFSEventStreamEventFlagItemModified) {
        mask |= (ORB_IN_MODIFY | ORB_IN_CLOSE_WRITE);
    }
    if (flags & kFSEventStreamEventFlagItemInodeMetaMod) {
        mask |= ORB_IN_ATTRIB;
    }
    if (flags & kFSEventStreamEventFlagItemRenamed) {
        mask |= ORB_IN_MOVE_SELF;
    }
    if (flags & kFSEventStreamEventFlagItemIsDir) {
        mask |= ORB_IN_ISDIR;
    }

    /* Fallback if item changed with unknown flags */
    if (mask == 0) {
        mask = (ORB_IN_MODIFY | ORB_IN_ATTRIB);
    }

    return mask;
}

static void fsevents_callback(
    ConstFSEventStreamRef streamRef,
    void *clientCallBackInfo,
    size_t numEvents,
    void *eventPaths,
    const FSEventStreamEventFlags eventFlags[],
    const FSEventStreamEventId eventIds[])
{
    (void)streamRef;
    (void)clientCallBackInfo;
    (void)eventIds;

    char **paths = (char **)eventPaths;
    uint64_t now_ns = get_host_time_ns();

    for (size_t i = 0; i < numEvents; i++) {
        const char *full_path = paths[i];
        FSEventStreamEventFlags flags = eventFlags[i];

        /* Skip events outside watch path */
        if (strncmp(full_path, g_watch_path, g_watch_path_len) != 0) {
            continue;
        }

        const char *rel_path = full_path + g_watch_path_len;
        while (*rel_path == '/') {
            rel_path++;
        }

        uint32_t inotify_mask = map_fsevents_to_inotify(flags);
        uint32_t path_len = (uint32_t)strlen(rel_path);

        orb_event_header_t hdr;
        hdr.magic = ORB_FS_MAGIC;
        hdr.version = ORB_FS_VERSION;
        hdr.opcode = ORB_OP_EVENT;
        hdr.inotify_mask = inotify_mask;
        hdr.seq = ++g_event_seq;
        hdr.timestamp_ns = now_ns;
        hdr.path_len = path_len;

        if (g_sock_fd >= 0) {
            /* Transmit header + path */
            struct iovec iov[2];
            iov[0].iov_base = &hdr;
            iov[0].iov_len = sizeof(hdr);
            iov[1].iov_base = (void *)rel_path;
            iov[1].iov_len = path_len;

            struct msghdr msg;
            memset(&msg, 0, sizeof(msg));
            msg.msg_iov = iov;
            msg.msg_iovlen = 2;

            ssize_t sent = sendmsg(g_sock_fd, &msg, 0);
            if (sent < 0) {
                perror("sendmsg to guest failed");
            }
        }
    }
}

int main(int argc, char *argv[]) {
    if (argc < 3) {
        fprintf(stderr, "Usage: %s <watch_directory> <bridge_port> [target_ip]\n", argv[0]);
        return 1;
    }

    const char *dir = argv[1];
    int port = atoi(argv[2]);
    const char *ip = (argc >= 4) ? argv[3] : "127.0.0.1";

    if (!realpath(dir, g_watch_path)) {
        perror("realpath");
        return 1;
    }
    g_watch_path_len = strlen(g_watch_path);
    printf("[Host FSEvents Bridge] Monitoring directory: %s\n", g_watch_path);
    printf("[Host FSEvents Bridge] Connecting to guest event daemon on %s:%d...\n", ip, port);

    int flag = 1;
    struct sockaddr_in serv_addr;
    memset(&serv_addr, 0, sizeof(serv_addr));
    serv_addr.sin_family = AF_INET;
    serv_addr.sin_port = htons(port);
    inet_pton(AF_INET, ip, &serv_addr.sin_addr);

    int retries = 50;
    while (1) {
        g_sock_fd = socket(AF_INET, SOCK_STREAM, 0);
        if (g_sock_fd < 0) {
            perror("socket");
            return 1;
        }
        setsockopt(g_sock_fd, IPPROTO_TCP, TCP_NODELAY, (char *)&flag, sizeof(int));

        if (connect(g_sock_fd, (struct sockaddr *)&serv_addr, sizeof(serv_addr)) == 0) {
            break;
        }

        close(g_sock_fd);
        g_sock_fd = -1;

        if (--retries == 0) {
            perror("connect to guest daemon failed");
            return 1;
        }
        usleep(100000); /* 100ms */
    }
    printf("[Host FSEvents Bridge] Connected to guest daemon successfully!\n");

    /* Create FSEventStream */
    CFStringRef pathStr = CFStringCreateWithCString(kCFAllocatorDefault, g_watch_path, kCFStringEncodingUTF8);
    CFArrayRef pathsToWatch = CFArrayCreate(kCFAllocatorDefault, (const void **)&pathStr, 1, NULL);

    FSEventStreamContext context;
    memset(&context, 0, sizeof(context));

    /* Immediate latency (0.0001s) with no deferral for real-time responsiveness */
    FSEventStreamCreateFlags streamFlags =
        kFSEventStreamCreateFlagFileEvents |
        kFSEventStreamCreateFlagNoDefer |
        kFSEventStreamCreateFlagWatchRoot;

    FSEventStreamRef stream = FSEventStreamCreate(
        kCFAllocatorDefault,
        fsevents_callback,
        &context,
        pathsToWatch,
        kFSEventStreamEventIdSinceNow,
        0.0001, /* 100 microseconds latency */
        streamFlags
    );

    dispatch_queue_t queue = dispatch_queue_create("com.orbstack_free.fsevents", DISPATCH_QUEUE_SERIAL_WITH_AUTORELEASE_POOL);
    dispatch_set_target_queue(queue, dispatch_get_global_queue(DISPATCH_QUEUE_PRIORITY_HIGH, 0));

    FSEventStreamSetDispatchQueue(stream, queue);
    FSEventStreamStart(stream);

    printf("[Host FSEvents Bridge] FSEventStream started with high-priority GCD dispatch queue.\n");

    /* Keep process running */
    dispatch_main();

    return 0;
}
