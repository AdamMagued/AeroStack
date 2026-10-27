#define _GNU_SOURCE
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
#include <fcntl.h>
#include <time.h>
#include <sys/stat.h>
#include <errno.h>

#ifdef __APPLE__
#include <sys/fcntl.h>
#define APPLY_NOCACHE(fd) fcntl(fd, F_NOCACHE, 1)
#else
#define APPLY_NOCACHE(fd) 0
#endif

#define SEQ_BUFFER_SIZE  (1024 * 1024)      /* 1 MB */
#define SEQ_FILE_SIZE    (64 * 1024 * 1024) /* 64 MB */
#define RAND_OPS_COUNT   2000
#define BLOCK_4K         4096
#define META_COUNT       500

static inline double now_sec(void) {
    struct timespec ts;
    clock_gettime(CLOCK_MONOTONIC, &ts);
    return (double)ts.tv_sec + (double)ts.tv_nsec / 1e9;
}

void bench_seq_write(const char *dir, int direct_io) {
    char path[1024];
    snprintf(path, sizeof(path), "%s/seq_write_%d.bin", dir, getpid());

    char *buf = malloc(SEQ_BUFFER_SIZE);
    memset(buf, 0x55, SEQ_BUFFER_SIZE);

    int flags = O_WRONLY | O_CREAT | O_TRUNC;
#ifdef O_DIRECT
    if (direct_io) flags |= O_DIRECT;
#endif

    int fd = open(path, flags, 0644);
    if (fd < 0) {
        perror("open seq_write");
        free(buf);
        return;
    }
    if (direct_io) {
        APPLY_NOCACHE(fd);
    }

    int blocks = SEQ_FILE_SIZE / SEQ_BUFFER_SIZE;
    double t0 = now_sec();
    for (int i = 0; i < blocks; i++) {
        ssize_t w = write(fd, buf, SEQ_BUFFER_SIZE);
        if (w != SEQ_BUFFER_SIZE) break;
    }
    fsync(fd);
    double t1 = now_sec();
    close(fd);

    double elapsed = t1 - t0;
    double mb = (double)SEQ_FILE_SIZE / (1024.0 * 1024.0);
    double rate = mb / elapsed;

    printf("  Sequential Write (%s, 64MB):     %8.2f MB/s (%6.3f s)\n",
           direct_io ? "Direct/Uncached" : "Buffered/Cached", rate, elapsed);
    unlink(path);
    free(buf);
}

void bench_seq_read(const char *dir, int direct_io) {
    char path[1024];
    snprintf(path, sizeof(path), "%s/seq_read_%d.bin", dir, getpid());

    /* Pre-write file */
    char *buf = malloc(SEQ_BUFFER_SIZE);
    memset(buf, 0xAA, SEQ_BUFFER_SIZE);
    int fd = open(path, O_WRONLY | O_CREAT | O_TRUNC, 0644);
    int blocks = SEQ_FILE_SIZE / SEQ_BUFFER_SIZE;
    for (int i = 0; i < blocks; i++) {
        write(fd, buf, SEQ_BUFFER_SIZE);
    }
    fsync(fd);
    close(fd);

    /* Read test */
    int flags = O_RDONLY;
#ifdef O_DIRECT
    if (direct_io) flags |= O_DIRECT;
#endif
    fd = open(path, flags);
    if (fd < 0) {
        perror("open seq_read");
        free(buf);
        return;
    }
    if (direct_io) {
        APPLY_NOCACHE(fd);
    }

    double t0 = now_sec();
    for (int i = 0; i < blocks; i++) {
        ssize_t r = read(fd, buf, SEQ_BUFFER_SIZE);
        if (r != SEQ_BUFFER_SIZE) break;
    }
    double t1 = now_sec();
    close(fd);

    double elapsed = t1 - t0;
    double mb = (double)SEQ_FILE_SIZE / (1024.0 * 1024.0);
    double rate = mb / elapsed;

    printf("  Sequential Read  (%s, 64MB):     %8.2f MB/s (%6.3f s)\n",
           direct_io ? "Direct/Uncached" : "Buffered/Cached", rate, elapsed);
    unlink(path);
    free(buf);
}

void bench_rand_4k(const char *dir, int direct_io) {
    char path[1024];
    snprintf(path, sizeof(path), "%s/rand_4k_%d.bin", dir, getpid());

    off_t file_len = 32 * 1024 * 1024;
    int fd = open(path, O_RDWR | O_CREAT | O_TRUNC, 0644);
    ftruncate(fd, file_len);

    char buf[BLOCK_4K];
    memset(buf, 0xCC, sizeof(buf));

    if (direct_io) {
        APPLY_NOCACHE(fd);
    }

    int max_blocks = file_len / BLOCK_4K;

    /* Write IOPS */
    double t0 = now_sec();
    for (int i = 0; i < RAND_OPS_COUNT; i++) {
        off_t off = (rand() % max_blocks) * BLOCK_4K;
        pwrite(fd, buf, BLOCK_4K, off);
    }
    if (direct_io) fsync(fd);
    double t1 = now_sec();

    double write_elapsed = t1 - t0;
    double write_iops = (double)RAND_OPS_COUNT / write_elapsed;

    /* Read IOPS */
    double t2 = now_sec();
    for (int i = 0; i < RAND_OPS_COUNT; i++) {
        off_t off = (rand() % max_blocks) * BLOCK_4K;
        pread(fd, buf, BLOCK_4K, off);
    }
    double t3 = now_sec();

    double read_elapsed = t3 - t2;
    double read_iops = (double)RAND_OPS_COUNT / read_elapsed;

    close(fd);
    unlink(path);

    printf("  4KB Random Write (%s, %d ops): %8.2f IOPS (%6.2f MB/s)\n",
           direct_io ? "Direct/Uncached" : "Buffered/Cached", RAND_OPS_COUNT, write_iops, (write_iops * BLOCK_4K) / (1024.0 * 1024.0));
    printf("  4KB Random Read  (%s, %d ops): %8.2f IOPS (%6.2f MB/s)\n",
           direct_io ? "Direct/Uncached" : "Buffered/Cached", RAND_OPS_COUNT, read_iops, (read_iops * BLOCK_4K) / (1024.0 * 1024.0));
}

int main(int argc, char *argv[]) {
    const char *test_dir = (argc >= 2) ? argv[1] : "/tmp";
    srand((unsigned int)time(NULL));

    printf("================================================================================\n");
    printf("ROUND 3 ADVERSARIAL AUDIT: VIRTIOFS / APFS FILESYSTEM I/O PERFORMANCE\n");
    printf("Testing Path: %s\n", test_dir);
    printf("================================================================================\n");

    printf("\n--- Test Suite A: Buffered / Cached I/O (Darwin Unified Buffer Cache) ---\n");
    bench_seq_write(test_dir, 0);
    bench_seq_read(test_dir, 0);
    bench_rand_4k(test_dir, 0);

    printf("\n--- Test Suite B: Direct / Uncached I/O (Bypassing Host & Guest Caches) ---\n");
    bench_seq_write(test_dir, 1);
    bench_seq_read(test_dir, 1);
    bench_rand_4k(test_dir, 1);

    printf("================================================================================\n");
    return 0;
}
