#define _GNU_SOURCE
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
#include <fcntl.h>
#include <time.h>
#include <sys/stat.h>
#include <sys/time.h>
#include <errno.h>

#define SEQ_FILE_SIZE   (64 * 1024 * 1024)   /* 64 MB */
#define BLOCK_SIZE_SEQ  (1024 * 1024)        /* 1 MB */
#define NUM_RAND_OPS    200                  /* 200 ops */
#define BLOCK_SIZE_4K   4096
#define NUM_META_FILES  200                  /* 200 files */

static inline double get_time_sec(void) {
    struct timespec ts;
    clock_gettime(CLOCK_MONOTONIC, &ts);
    return (double)ts.tv_sec + (double)ts.tv_nsec / 1e9;
}

void benchmark_sequential_write(const char *dir) {
    char path[1024];
    snprintf(path, sizeof(path), "%s/bench_seq_%d.bin", dir, getpid());
    
    char *buf = malloc(BLOCK_SIZE_SEQ);
    memset(buf, 0xAA, BLOCK_SIZE_SEQ);

    int fd = open(path, O_WRONLY | O_CREAT | O_TRUNC, 0644);
    if (fd < 0) { 
        fprintf(stderr, "open seq write failed on path: '%s': %s\n", path, strerror(errno));
        free(buf); 
        return; 
    }

    int num_blocks = SEQ_FILE_SIZE / BLOCK_SIZE_SEQ;
    double t0 = get_time_sec();

    for (int i = 0; i < num_blocks; i++) {
        ssize_t w = write(fd, buf, BLOCK_SIZE_SEQ);
        if (w != BLOCK_SIZE_SEQ) { perror("write"); break; }
    }
    fsync(fd);
    double t1 = get_time_sec();
    close(fd);

    double elapsed = t1 - t0;
    double mb = (double)SEQ_FILE_SIZE / (1024.0 * 1024.0);
    double throughput = mb / elapsed;

    printf("  Sequential Write (256MB, 1MB block): %8.2f MB/s (%6.3f sec)\n", throughput, elapsed);
    fflush(stdout);
    free(buf);
}

void benchmark_sequential_read(const char *dir) {
    char path[1024];
    snprintf(path, sizeof(path), "%s/bench_seq_%d.bin", dir, getpid());

    char *buf = malloc(BLOCK_SIZE_SEQ);
    int fd = open(path, O_RDONLY);
    if (fd < 0) { perror("open seq read"); free(buf); return; }

    int num_blocks = SEQ_FILE_SIZE / BLOCK_SIZE_SEQ;
    double t0 = get_time_sec();

    for (int i = 0; i < num_blocks; i++) {
        ssize_t r = read(fd, buf, BLOCK_SIZE_SEQ);
        if (r != BLOCK_SIZE_SEQ) { break; }
    }
    double t1 = get_time_sec();
    close(fd);

    double elapsed = t1 - t0;
    double mb = (double)SEQ_FILE_SIZE / (1024.0 * 1024.0);
    double throughput = mb / elapsed;

    printf("  Sequential Read  (64MB, 1MB block):  %8.2f MB/s (%6.3f sec)\n", throughput, elapsed);
    fflush(stdout);
    unlink(path);
    free(buf);
}

void benchmark_random_4k_write(const char *dir) {
    char path[1024];
    snprintf(path, sizeof(path), "%s/bench_rand_%d.bin", dir, getpid());

    char buf[BLOCK_SIZE_4K];
    memset(buf, 0x55, sizeof(buf));

    int fd = open(path, O_RDWR | O_CREAT | O_TRUNC, 0644);
    if (fd < 0) { perror("open rand write"); return; }

    /* Pre-allocate 32MB file */
    off_t file_len = 32 * 1024 * 1024;
    ftruncate(fd, file_len);

    int max_blocks = file_len / BLOCK_SIZE_4K;
    double t0 = get_time_sec();

    for (int i = 0; i < NUM_RAND_OPS; i++) {
        off_t offset = (rand() % max_blocks) * BLOCK_SIZE_4K;
        pwrite(fd, buf, BLOCK_SIZE_4K, offset);
    }
    fsync(fd);
    double t1 = get_time_sec();
    close(fd);

    double elapsed = t1 - t0;
    double iops = (double)NUM_RAND_OPS / elapsed;
    double mb_s = (iops * BLOCK_SIZE_4K) / (1024.0 * 1024.0);

    printf("  Random 4KB Write (%d ops):           %8.2f IOPS (%6.2f MB/s)\n", NUM_RAND_OPS, iops, mb_s);
    fflush(stdout);
}

void benchmark_random_4k_read(const char *dir) {
    char path[1024];
    snprintf(path, sizeof(path), "%s/bench_rand_%d.bin", dir, getpid());

    char buf[BLOCK_SIZE_4K];
    int fd = open(path, O_RDONLY);
    if (fd < 0) { perror("open rand read"); return; }

    off_t file_len = 32 * 1024 * 1024;
    int max_blocks = file_len / BLOCK_SIZE_4K;
    double t0 = get_time_sec();

    for (int i = 0; i < NUM_RAND_OPS; i++) {
        off_t offset = (rand() % max_blocks) * BLOCK_SIZE_4K;
        pread(fd, buf, BLOCK_SIZE_4K, offset);
    }
    double t1 = get_time_sec();
    close(fd);

    double elapsed = t1 - t0;
    double iops = (double)NUM_RAND_OPS / elapsed;
    double mb_s = (iops * BLOCK_SIZE_4K) / (1024.0 * 1024.0);

    printf("  Random 4KB Read  (%d ops):           %8.2f IOPS (%6.2f MB/s)\n", NUM_RAND_OPS, iops, mb_s);
    fflush(stdout);
    unlink(path);
}

void benchmark_metadata_ops(const char *dir) {
    char meta_dir[1024];
    snprintf(meta_dir, sizeof(meta_dir), "%s/meta_%d", dir, getpid());
    mkdir(meta_dir, 0755);

    /* 1. Create */
    double t0 = get_time_sec();
    for (int i = 0; i < NUM_META_FILES; i++) {
        char fpath[1024];
        snprintf(fpath, sizeof(fpath), "%s/f_%d.tmp", meta_dir, i);
        int fd = open(fpath, O_WRONLY | O_CREAT | O_TRUNC, 0644);
        if (fd >= 0) {
            write(fd, "test", 4);
            close(fd);
        }
    }
    double t1 = get_time_sec();
    double create_rate = (double)NUM_META_FILES / (t1 - t0);
    printf("  File Create Rate (%d files):        %8.2f files/sec (%6.3f sec)\n", NUM_META_FILES, create_rate, t1 - t0);
    fflush(stdout);

    /* 2. Stat */
    double t2 = get_time_sec();
    for (int i = 0; i < NUM_META_FILES; i++) {
        char fpath[1024];
        snprintf(fpath, sizeof(fpath), "%s/f_%d.tmp", meta_dir, i);
        struct stat st;
        stat(fpath, &st);
    }
    double t3 = get_time_sec();
    double stat_rate = (double)NUM_META_FILES / (t3 - t2);
    printf("  File Stat Rate   (%d files):        %8.2f stats/sec (%6.3f sec)\n", NUM_META_FILES, stat_rate, t3 - t2);
    fflush(stdout);

    /* 3. Unlink */
    double t4 = get_time_sec();
    for (int i = 0; i < NUM_META_FILES; i++) {
        char fpath[1024];
        snprintf(fpath, sizeof(fpath), "%s/f_%d.tmp", meta_dir, i);
        unlink(fpath);
    }
    double t5 = get_time_sec();
    double unlink_rate = (double)NUM_META_FILES / (t5 - t4);
    printf("  File Unlink Rate (%d files):        %8.2f unlinks/sec (%6.3f sec)\n", NUM_META_FILES, unlink_rate, t5 - t4);
    fflush(stdout);

    rmdir(meta_dir);
}

int main(int argc, char *argv[]) {
    const char *test_dir = (argc >= 2) ? argv[1] : ".";
    const char *label = (argc >= 3) ? argv[2] : "Target Directory";

    printf("\n======================================================================\n");
    printf(" BENCHMARK: %s\n", label);
    printf(" Path: %s\n", test_dir);
    printf("======================================================================\n");
    fflush(stdout);

    benchmark_sequential_write(test_dir);
    benchmark_sequential_read(test_dir);
    benchmark_random_4k_write(test_dir);
    benchmark_random_4k_read(test_dir);
    benchmark_metadata_ops(test_dir);

    printf("======================================================================\n\n");
    return 0;
}
