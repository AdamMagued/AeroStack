#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
#include <sys/socket.h>
#include <sys/time.h>
#include <pthread.h>

#define NUM_PACKETS 500000

typedef struct {
    int fd;
    size_t mtu;
    size_t total_packets;
} WorkerArgs;

static void *receiver_thread(void *arg) {
    WorkerArgs *args = (WorkerArgs *)arg;
    char *buf = malloc(args->mtu + 128);
    size_t received = 0;
    
    while (received < args->total_packets) {
        ssize_t n = recv(args->fd, buf, args->mtu, 0);
        if (n <= 0) break;
        received++;
    }
    
    free(buf);
    return (void *)received;
}

static double get_time_sec() {
    struct timeval tv;
    gettimeofday(&tv, NULL);
    return tv.tv_sec + tv.tv_usec * 1e-6;
}

void run_throughput_benchmark(size_t mtu, int tune_buffers) {
    int fds[2];
    if (socketpair(AF_UNIX, SOCK_DGRAM, 0, fds) < 0) {
        perror("socketpair");
        return;
    }

    if (tune_buffers) {
        // As recommended by Apple Virtualization.framework:
        // SO_RCVBUF = 4 * SO_SNDBUF (e.g. 1MB SND, 4MB RCV)
        int sndbuf = 1024 * 1024;
        int rcvbuf = 4 * 1024 * 1024;
        setsockopt(fds[0], SOL_SOCKET, SO_SNDBUF, &sndbuf, sizeof(sndbuf));
        setsockopt(fds[0], SOL_SOCKET, SO_RCVBUF, &rcvbuf, sizeof(rcvbuf));
        setsockopt(fds[1], SOL_SOCKET, SO_SNDBUF, &sndbuf, sizeof(sndbuf));
        setsockopt(fds[1], SOL_SOCKET, SO_RCVBUF, &rcvbuf, sizeof(rcvbuf));
    }

    pthread_t th;
    WorkerArgs args = { .fd = fds[1], .mtu = mtu, .total_packets = NUM_PACKETS };
    pthread_create(&th, NULL, receiver_thread, &args);

    char *send_buf = malloc(mtu);
    memset(send_buf, 0xAB, mtu);

    double start = get_time_sec();
    size_t sent = 0;
    for (size_t i = 0; i < NUM_PACKETS; i++) {
        ssize_t n = send(fds[0], send_buf, mtu, 0);
        if (n > 0) sent++;
        else {
            // Buffer full, yield briefly
            usleep(1);
            i--;
        }
    }

    pthread_join(th, NULL);
    double elapsed = get_time_sec() - start;

    double total_bytes = (double)sent * mtu;
    double gbps = (total_bytes * 8.0) / (elapsed * 1e9);
    double mpps = (double)sent / (elapsed * 1e6);

    printf("MTU: %5zu | Tuned: %d | Sent: %zu pkts | Time: %.3fs | %.2f Mpps | Throughput: %.2f Gbps (%.2f MB/s)\n",
           mtu, tune_buffers, sent, elapsed, mpps, gbps, (total_bytes / (1024*1024)) / elapsed);

    free(send_buf);
    close(fds[0]);
    close(fds[1]);
}

static void *echo_thread(void *arg) {
    int fd = *(int *)arg;
    char echo_buf[1500];
    const int round_trips = 100000;
    for (int i = 0; i < round_trips; i++) {
        ssize_t n = recv(fd, echo_buf, sizeof(echo_buf), 0);
        if (n <= 0) break;
        send(fd, echo_buf, n, 0);
    }
    return NULL;
}

void run_latency_benchmark() {
    int fds[2];
    if (socketpair(AF_UNIX, SOCK_DGRAM, 0, fds) < 0) {
        perror("socketpair");
        return;
    }

    char buf[1500];
    const int round_trips = 100000;

    pthread_t th;
    pthread_create(&th, NULL, echo_thread, &fds[1]);

    double start = get_time_sec();
    for (int i = 0; i < round_trips; i++) {
        send(fds[0], buf, 64, 0); // 64 byte ICMP-like packet
        recv(fds[0], buf, sizeof(buf), 0);
    }
    double elapsed = get_time_sec() - start;
    pthread_join(th, NULL);

    double avg_rtt_us = (elapsed / round_trips) * 1e6;
    printf("Round-trip Ping-Pong Latency (64 bytes): %.2f µs (microsecond per round-trip)\n", avg_rtt_us);

    close(fds[0]);
    close(fds[1]);
}

int main() {
    printf("================================================================================\n");
    printf("  Bi-Directional Datapath Micro-Benchmark: VZFileHandleNetworkDeviceAttachment  \n");
    printf("================================================================================\n\n");

    printf("--- Latency Measurement ---\n");
    run_latency_benchmark();

    printf("\n--- Throughput Measurement ---\n");
    run_throughput_benchmark(1500, 0);
    run_throughput_benchmark(1500, 1);
    run_throughput_benchmark(16384, 1);
    run_throughput_benchmark(65535, 1);

    return 0;
}
