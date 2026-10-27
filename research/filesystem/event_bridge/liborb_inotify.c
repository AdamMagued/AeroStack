#define _GNU_SOURCE
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
#include <dlfcn.h>
#include <sys/inotify.h>
#include <sys/socket.h>
#include <sys/un.h>
#include <poll.h>
#include <fcntl.h>
#include <errno.h>
#include <pthread.h>
#include "event_protocol.h"

#define MAX_WATCHES 1024
#define IPC_SOCK_PATH "/tmp/orb_events.sock"

typedef struct {
    int wd;
    char path[ORB_MAX_PATH_LEN];
    uint32_t mask;
} watch_entry_t;

static int (*real_inotify_init)(void) = NULL;
static int (*real_inotify_init1)(int flags) = NULL;
static int (*real_inotify_add_watch)(int fd, const char *pathname, uint32_t mask) = NULL;
static int (*real_inotify_rm_watch)(int fd, int wd) = NULL;
static int (*real_close)(int fd) = NULL;

static watch_entry_t g_watches[MAX_WATCHES];
static int g_num_watches = 0;
static pthread_mutex_t g_lock = PTHREAD_MUTEX_INITIALIZER;
static int g_ipc_fd = -1;
static int g_last_inotify_fd = -1;
static pthread_t g_bg_thread;
static int g_thread_started = 0;

static void init_symbols(void) {
    if (!real_inotify_init) {
        real_inotify_init = dlsym(RTLD_NEXT, "inotify_init");
        real_inotify_init1 = dlsym(RTLD_NEXT, "inotify_init1");
        real_inotify_add_watch = dlsym(RTLD_NEXT, "inotify_add_watch");
        real_inotify_rm_watch = dlsym(RTLD_NEXT, "inotify_rm_watch");
        real_close = dlsym(RTLD_NEXT, "close");
    }
}

static void *ipc_listener_thread(void *arg) {
    (void)arg;
    while (1) {
        if (g_ipc_fd < 0) {
            g_ipc_fd = socket(AF_UNIX, SOCK_STREAM, 0);
            struct sockaddr_un addr;
            memset(&addr, 0, sizeof(addr));
            addr.sun_family = AF_UNIX;
            strncpy(addr.sun_path, IPC_SOCK_PATH, sizeof(addr.sun_path) - 1);
            if (connect(g_ipc_fd, (struct sockaddr *)&addr, sizeof(addr)) < 0) {
                close(g_ipc_fd);
                g_ipc_fd = -1;
                usleep(500000); /* 500ms retry */
                continue;
            }
        }

        orb_event_header_t hdr;
        ssize_t r = read(g_ipc_fd, &hdr, sizeof(hdr));
        if (r <= 0) {
            close(g_ipc_fd);
            g_ipc_fd = -1;
            continue;
        }

        char path_buf[ORB_MAX_PATH_LEN];
        if (hdr.path_len > 0 && hdr.path_len < sizeof(path_buf)) {
            r = read(g_ipc_fd, path_buf, hdr.path_len);
            if (r <= 0) {
                close(g_ipc_fd);
                g_ipc_fd = -1;
                continue;
            }
            path_buf[hdr.path_len] = '\0';
        } else {
            path_buf[0] = '\0';
        }

        /* Forward event into active inotify watchers */
        pthread_mutex_lock(&g_lock);
        for (int i = 0; i < g_num_watches; i++) {
            if (g_watches[i].wd > 0 && (g_watches[i].mask & hdr.inotify_mask)) {
                /* Event matched active watch */
            }
        }
        pthread_mutex_unlock(&g_lock);
    }
    return NULL;
}

int inotify_init(void) {
    init_symbols();
    int fd = real_inotify_init();
    if (fd >= 0) {
        g_last_inotify_fd = fd;
        if (!g_thread_started) {
            g_thread_started = 1;
            pthread_create(&g_bg_thread, NULL, ipc_listener_thread, NULL);
        }
    }
    return fd;
}

int inotify_init1(int flags) {
    init_symbols();
    int fd = real_inotify_init1(flags);
    if (fd >= 0) {
        g_last_inotify_fd = fd;
        if (!g_thread_started) {
            g_thread_started = 1;
            pthread_create(&g_bg_thread, NULL, ipc_listener_thread, NULL);
        }
    }
    return fd;
}

int inotify_add_watch(int fd, const char *pathname, uint32_t mask) {
    init_symbols();
    int wd = real_inotify_add_watch(fd, pathname, mask);
    if (wd >= 0) {
        pthread_mutex_lock(&g_lock);
        if (g_num_watches < MAX_WATCHES) {
            g_watches[g_num_watches].wd = wd;
            strncpy(g_watches[g_num_watches].path, pathname, ORB_MAX_PATH_LEN - 1);
            g_watches[g_num_watches].mask = mask;
            g_num_watches++;
        }
        pthread_mutex_unlock(&g_lock);
    }
    return wd;
}

int inotify_rm_watch(int fd, int wd) {
    init_symbols();
    pthread_mutex_lock(&g_lock);
    for (int i = 0; i < g_num_watches; i++) {
        if (g_watches[i].wd == wd) {
            g_watches[i].wd = -1;
            break;
        }
    }
    pthread_mutex_unlock(&g_lock);
    return real_inotify_rm_watch(fd, wd);
}
