#!/usr/bin/env python3
import os
import sys
import time
import subprocess
import re

TEST_DIR = "/Volumes/AdamSSD/antigravity_workspace/orbstack_free/research/filesystem/test_watch"
BRIDGE_DIR = "/Volumes/AdamSSD/antigravity_workspace/orbstack_free/research/filesystem/event_bridge"
TARGET_PREFIX = os.path.join(TEST_DIR, "file")
PORT = 9955
NUM_EVENTS = 30

def cleanup():
    subprocess.run(["docker", "rm", "-f", "orb_guest_test"], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    subprocess.run(["pkill", "-f", "host_fsevents_bridge"], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)

def main():
    print("=" * 75)
    print("      HIGH-PRECISION VIRTIOFS FILE EVENT SYNCHRONIZATION BENCHMARK")
    print("      macOS FSEvents -> Network/Vsock Bridge -> Linux Kernel Inotify")
    print("=" * 75)

    cleanup()
    os.makedirs(TEST_DIR, exist_ok=True)
    for f in os.listdir(TEST_DIR):
        try:
            os.remove(os.path.join(TEST_DIR, f))
        except:
            pass

    # 1. Start guest daemon in Docker
    print(f"\n[1/3] Launching Linux Guest Inotify Daemon on port {PORT}...")
    guest_cmd = [
        "docker", "run", "-d",
        "--name", "orb_guest_test",
        "-p", f"{PORT}:{PORT}",
        "-v", f"{TEST_DIR}:/test_watch",
        "-v", f"{BRIDGE_DIR}:/bridge",
        "orb-dev-env",
        "/bridge/guest_inotify_daemon", "/test_watch", str(PORT)
    ]
    container_id = subprocess.check_output(guest_cmd).decode().strip()
    print(f"      Guest Container: {container_id[:12]}")
    time.sleep(1.0)

    # 2. Start host FSEvents bridge
    print("[2/3] Starting macOS FSEvents Bridge (Swift)...")
    host_bridge_cmd = [
        os.path.join(BRIDGE_DIR, "host_fsevents_bridge_swift"),
        TEST_DIR,
        str(PORT),
        "127.0.0.1"
    ]
    host_proc = subprocess.Popen(host_bridge_cmd, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)
    time.sleep(1.5)

    # 3. Run host latency driver
    print(f"\n[3/3] Generating {NUM_EVENTS} distinct file events from macOS host...")
    driver_cmd = [
        os.path.join(BRIDGE_DIR, "host_latency_driver"),
        TARGET_PREFIX,
        str(NUM_EVENTS),
        "80"  # 80ms interval
    ]
    driver_proc = subprocess.run(driver_cmd, capture_output=True, text=True)
    print("      All writes committed to APFS. Waiting for ACKs...")
    time.sleep(2.0)

    # Kill host bridge cleanly
    host_proc.terminate()
    try:
        host_stdout, _ = host_proc.communicate(timeout=2.0)
    except:
        host_proc.kill()
        host_stdout, _ = host_proc.communicate()

    guest_logs = subprocess.check_output(["docker", "logs", "orb_guest_test"]).decode()
    cleanup()

    # Parse ACK RTT lines from host bridge output
    rtt_latencies_us = []
    oneway_latencies_us = []
    for line in host_stdout.splitlines():
        m = re.search(r"\[ACK_RTT #\d+\] RTT=([0-9.]+) us .* One-Way Delivery=([0-9.]+) ms", line)
        if m:
            rtt_us = float(m.group(1))
            oneway_ms = float(m.group(2))
            rtt_latencies_us.append(rtt_us)
            oneway_latencies_us.append(oneway_ms * 1000.0)

    print("\n" + "=" * 75)
    print("               RIGOROUS EMPIRICAL BENCHMARK METRICS")
    print("=" * 75)

    if not oneway_latencies_us:
        print("[!] No ACK samples received. Host stdout:")
        print(host_stdout)
        print("\nGuest logs:")
        print(guest_logs)
        return 1

    oneway_latencies_us.sort()
    rtt_latencies_us.sort()
    n = len(oneway_latencies_us)

    min_ow = oneway_latencies_us[0]
    p50_ow = oneway_latencies_us[n // 2]
    p95_ow = oneway_latencies_us[int(n * 0.95)]
    p99_ow = oneway_latencies_us[int(n * 0.99)]
    max_ow = oneway_latencies_us[-1]
    avg_ow = sum(oneway_latencies_us) / n

    min_rtt = rtt_latencies_us[0]
    p50_rtt = rtt_latencies_us[n // 2]
    p95_rtt = rtt_latencies_us[int(n * 0.95)]
    p99_rtt = rtt_latencies_us[int(n * 0.99)]
    max_rtt = rtt_latencies_us[-1]
    avg_rtt = sum(rtt_latencies_us) / n

    print(f"Total Events Measured:     {n} / {NUM_EVENTS}")
    print("\n--- ONE-WAY EVENT DELIVERY LATENCY (Host Write -> Guest Inotify Trigger) ---")
    print(f"  Minimum Latency:         {min_ow:8.2f} µs   ({min_ow / 1000.0:.4f} ms)")
    print(f"  Median (P50) Latency:    {p50_ow:8.2f} µs   ({p50_ow / 1000.0:.4f} ms)")
    print(f"  95th Percentile (P95):   {p95_ow:8.2f} µs   ({p95_ow / 1000.0:.4f} ms)")
    print(f"  99th Percentile (P99):   {p99_ow:8.2f} µs   ({p99_ow / 1000.0:.4f} ms)")
    print(f"  Maximum Latency:         {max_ow:8.2f} µs   ({max_ow / 1000.0:.4f} ms)")
    print(f"  Mean (Average) Latency:  {avg_ow:8.2f} µs   ({avg_ow / 1000.0:.4f} ms)")

    print("\n--- FULL ROUND-TRIP TIME (Host FSEvents -> Guest VFS -> Host ACK) ---")
    print(f"  Minimum RTT:             {min_rtt:8.2f} µs   ({min_rtt / 1000.0:.4f} ms)")
    print(f"  Median (P50) RTT:        {p50_rtt:8.2f} µs   ({p50_rtt / 1000.0:.4f} ms)")
    print(f"  95th Percentile (P95) RTT:{p95_rtt:8.2f} µs   ({p95_rtt / 1000.0:.4f} ms)")
    print(f"  99th Percentile (P99) RTT:{p99_rtt:8.2f} µs   ({p99_rtt / 1000.0:.4f} ms)")
    print(f"  Maximum RTT:             {max_rtt:8.2f} µs   ({max_rtt / 1000.0:.4f} ms)")
    print(f"  Mean (Average) RTT:      {avg_rtt:8.2f} µs   ({avg_rtt / 1000.0:.4f} ms)")
    print("-" * 75)

    if avg_ow / 1000.0 < 2.0:
        print(">>> RESULT: SUCCESS! ONE-WAY LATENCY < 2.0 ms REQUIREMENT FULFILLED! <<<")
    else:
        print(">>> RESULT: TARGET EXCEEDED <<<")
    print("=" * 75)

    # Save summary
    res_path = os.path.join(BRIDGE_DIR, "latency_benchmark_summary.txt")
    with open(res_path, "w") as f:
        f.write("ORBSTACK_FREE FILE EVENT SYNCHRONIZATION BENCHMARK\n")
        f.write(f"Timestamp: {time.ctime()}\n")
        f.write(f"Events: {n}\n")
        f.write(f"One-Way Min: {min_ow:.2f} us ({min_ow/1000.0:.4f} ms)\n")
        f.write(f"One-Way P50: {p50_ow:.2f} us ({p50_ow/1000.0:.4f} ms)\n")
        f.write(f"One-Way P95: {p95_ow:.2f} us ({p95_ow/1000.0:.4f} ms)\n")
        f.write(f"One-Way P99: {p99_ow:.2f} us ({p99_ow/1000.0:.4f} ms)\n")
        f.write(f"One-Way Max: {max_ow:.2f} us ({max_ow/1000.0:.4f} ms)\n")
        f.write(f"One-Way Mean: {avg_ow:.2f} us ({avg_ow/1000.0:.4f} ms)\n")
        f.write(f"RTT Min: {min_rtt:.2f} us ({min_rtt/1000.0:.4f} ms)\n")
        f.write(f"RTT P50: {p50_rtt:.2f} us ({p50_rtt/1000.0:.4f} ms)\n")
        f.write(f"RTT Mean: {avg_rtt:.2f} us ({avg_rtt/1000.0:.4f} ms)\n")
    print(f"Report saved to: {res_path}")
    return 0

if __name__ == "__main__":
    sys.exit(main())
