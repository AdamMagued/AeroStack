#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
#include <sys/mman.h>
#include <errno.h>
#include <mach/mach.h>
#include <mach/task_info.h>
#include <mach/mach_time.h>
#include <mach/mach_vm.h>

static mach_timebase_info_data_t tb;

static inline double tb_to_us(uint64_t elapsed) {
    return (double)elapsed * tb.numer / tb.denom / 1000.0;
}

static uint64_t get_phys_footprint(void) {
    task_vm_info_data_t vm_info;
    mach_msg_type_number_t count = TASK_VM_INFO_COUNT;
    if (task_info(mach_task_self(), TASK_VM_INFO, (task_info_t)&vm_info, &count) == KERN_SUCCESS) {
        return vm_info.phys_footprint;
    }
    return 0;
}

static uint64_t get_resident_size(void) {
    mach_task_basic_info_data_t basic_info;
    mach_msg_type_number_t count = MACH_TASK_BASIC_INFO_COUNT;
    if (task_info(mach_task_self(), MACH_TASK_BASIC_INFO, (task_info_t)&basic_info, &count) == KERN_SUCCESS) {
        return basic_info.resident_size;
    }
    return 0;
}

int main(void) {
    mach_timebase_info(&tb);
    size_t host_page_sz = (size_t)sysconf(_SC_PAGESIZE);
    printf("================================================================================\n");
    printf("Darwin Mach VM Page Granularity & Refault Latency Micro-Benchmark\n");
    printf("Host Architecture: Apple Silicon ARM64, Page Size = %zu bytes (%zu KB)\n",
           host_page_sz, host_page_sz / 1024);
    printf("================================================================================\n\n");

    // Benchmark 1: Measure exact per-page reclamation cost across 64MB (4096 host 16KB pages)
    size_t bench_sz = 64 * 1024 * 1024;
    size_t num_pages = bench_sz / host_page_sz;

    void *mem = mmap(NULL, bench_sz, PROT_READ | PROT_WRITE, MAP_ANON | MAP_PRIVATE, -1, 0);
    if (mem == MAP_FAILED) {
        perror("mmap");
        return 1;
    }

    uint64_t fp_base = get_phys_footprint();

    // 1. First touch / allocation latency
    uint64_t t0 = mach_absolute_time();
    for (size_t i = 0; i < num_pages; i++) {
        *((volatile uint64_t *)((char *)mem + i * host_page_sz)) = 0xDEADBEEF;
    }
    uint64_t t1 = mach_absolute_time();
    uint64_t fp_dirtied = get_phys_footprint();
    double first_touch_us = tb_to_us(t1 - t0);
    printf("[1] First Touch / Page Fault (64MB = %zu pages):\n", num_pages);
    printf("    Latency: %.2f us total (%.3f us/page, %.2f GB/s)\n",
           first_touch_us, first_touch_us / num_pages,
           (64.0 / 1024.0) / (first_touch_us / 1e6));
    printf("    Footprint: +%.2f MB (Resident: %.2f MB)\n\n",
           (double)(fp_dirtied - fp_base) / (1024.0 * 1024.0),
           (double)get_resident_size() / (1024.0 * 1024.0));

    // 2. MADV_FREE_REUSABLE (Virtio-balloon mechanism)
    t0 = mach_absolute_time();
    int ret = madvise(mem, bench_sz, MADV_FREE_REUSABLE);
    t1 = mach_absolute_time();
    uint64_t fp_reusable = get_phys_footprint();
    double reusable_us = tb_to_us(t1 - t0);
    printf("[2] MADV_FREE_REUSABLE (Marking for Hypervisor Reclamation):\n");
    printf("    Return: %d, Latency: %.2f us (%.3f us/page, %.2f GB/s)\n",
           ret, reusable_us, reusable_us / num_pages,
           (64.0 / 1024.0) / (reusable_us / 1e6));
    printf("    Footprint: %.2f MB (Δ %+.2f MB, Resident: %.2f MB)\n\n",
           (double)fp_reusable / (1024.0 * 1024.0),
           (double)((int64_t)fp_reusable - (int64_t)fp_dirtied) / (1024.0 * 1024.0),
           (double)get_resident_size() / (1024.0 * 1024.0));

    // 3. Re-fault latency while marked REUSABLE
    t0 = mach_absolute_time();
    for (size_t i = 0; i < num_pages; i++) {
        volatile uint64_t val = *((volatile uint64_t *)((char *)mem + i * host_page_sz));
        (void)val;
    }
    t1 = mach_absolute_time();
    uint64_t fp_after_read = get_phys_footprint();
    double read_us = tb_to_us(t1 - t0);
    printf("[3] Re-reading pages without un-marking:\n");
    printf("    Latency: %.2f us total (%.3f us/page)\n", read_us, read_us / num_pages);
    printf("    Footprint: %.2f MB (Footprint remains uncharged or unchanged: %+.2f MB)\n\n",
           (double)fp_after_read / (1024.0 * 1024.0),
           (double)((int64_t)fp_after_read - (int64_t)fp_reusable) / (1024.0 * 1024.0));

    // 4. MADV_FREE_REUSE (Deflation / reclaim back to guest)
    t0 = mach_absolute_time();
    ret = madvise(mem, bench_sz, MADV_FREE_REUSE);
    t1 = mach_absolute_time();
    uint64_t fp_reused = get_phys_footprint();
    double reuse_us = tb_to_us(t1 - t0);
    printf("[4] MADV_FREE_REUSE (Deflating back to guest):\n");
    printf("    Return: %d, Latency: %.2f us (%.3f us/page)\n",
           ret, reuse_us, reuse_us / num_pages);
    printf("    Footprint: %.2f MB (Δ %+.2f MB restored)\n\n",
           (double)fp_reused / (1024.0 * 1024.0),
           (double)((int64_t)fp_reused - (int64_t)fp_reusable) / (1024.0 * 1024.0));

    // 5. Partial / Sub-page Granularity Test:
    // What if guest Linux has 4KB pages and only frees 4KB out of a 16KB host page?
    printf("[5] Sub-page (4KB vs 16KB) Granularity Experiment:\n");
    void *sub_mem = mmap(NULL, host_page_sz * 100, PROT_READ | PROT_WRITE, MAP_ANON | MAP_PRIVATE, -1, 0);
    memset(sub_mem, 0x11, host_page_sz * 100);
    uint64_t fp_sub_dirtied = get_phys_footprint();

    // Try madvise on single 4KB chunk (offset 0, length 4096)
    ret = madvise(sub_mem, 4096, MADV_FREE_REUSABLE);
    uint64_t fp_sub_reusable = get_phys_footprint();
    printf("    madvise(MADV_FREE_REUSABLE, 4KB on 16KB host): ret=%d\n", ret);
    printf("    Footprint delta: %+.2f KB (Did host free partial 16KB page? %s)\n",
           (double)((int64_t)fp_sub_reusable - (int64_t)fp_sub_dirtied) / 1024.0,
           fp_sub_reusable < fp_sub_dirtied ? "YES" : "NO - Darwin ignores sub-host pages!");

    munmap(mem, bench_sz);
    munmap(sub_mem, host_page_sz * 100);
    return 0;
}
