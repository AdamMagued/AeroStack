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

typedef struct {
    uint64_t resident_size;
    uint64_t phys_footprint;
    uint64_t internal_bytes;
    uint64_t compressed_bytes;
} MemoryStats;

static MemoryStats get_memory_stats(void) {
    MemoryStats stats = {0};
    mach_msg_type_number_t count;
    
    // mach_task_basic_info
    mach_task_basic_info_data_t basic_info;
    count = MACH_TASK_BASIC_INFO_COUNT;
    if (task_info(mach_task_self(), MACH_TASK_BASIC_INFO, (task_info_t)&basic_info, &count) == KERN_SUCCESS) {
        stats.resident_size = basic_info.resident_size;
    }
    
    // task_vm_info
    task_vm_info_data_t vm_info;
    count = TASK_VM_INFO_COUNT;
    if (task_info(mach_task_self(), TASK_VM_INFO, (task_info_t)&vm_info, &count) == KERN_SUCCESS) {
        stats.phys_footprint = vm_info.phys_footprint;
        stats.internal_bytes = vm_info.internal;
        stats.compressed_bytes = vm_info.compressed;
    }
    
    return stats;
}

static void print_stats(const char *label, MemoryStats before, MemoryStats current) {
    int64_t d_res = (int64_t)current.resident_size - (int64_t)before.resident_size;
    int64_t d_fp  = (int64_t)current.phys_footprint - (int64_t)before.phys_footprint;
    printf("%-35s | Resident: %8.2f MB (Δ %+.2f MB) | Footprint: %8.2f MB (Δ %+.2f MB)\n",
           label,
           (double)current.resident_size / (1024.0 * 1024.0),
           (double)d_res / (1024.0 * 1024.0),
           (double)current.phys_footprint / (1024.0 * 1024.0),
           (double)d_fp / (1024.0 * 1024.0));
}

int main(void) {
    size_t page_sz = (size_t)sysconf(_SC_PAGESIZE);
    printf("================================================================================\n");
    printf("Darwin Mach VM Memory Reclamation Micro-Benchmark\n");
    printf("Host Page Size: %zu bytes (%zu KB)\n", page_sz, page_sz / 1024);
    printf("================================================================================\n\n");

    mach_timebase_info_data_t timebase;
    mach_timebase_info(&timebase);

    MemoryStats base = get_memory_stats();
    print_stats("Baseline", base, base);

    // Test 1: Allocate 256MB anonymous memory
    size_t alloc_sz = 256 * 1024 * 1024;
    void *ptr = mmap(NULL, alloc_sz, PROT_READ | PROT_WRITE, MAP_ANON | MAP_PRIVATE, -1, 0);
    if (ptr == MAP_FAILED) {
        perror("mmap");
        return 1;
    }
    MemoryStats after_mmap = get_memory_stats();
    print_stats("After mmap(MAP_ANON 256MB)", base, after_mmap);

    // Test 2: Dirty all 256MB
    uint64_t t0 = mach_absolute_time();
    memset(ptr, 0xAA, alloc_sz);
    uint64_t t1 = mach_absolute_time();
    double touch_ms = (double)(t1 - t0) * timebase.numer / timebase.denom / 1e6;
    MemoryStats after_dirty = get_memory_stats();
    print_stats("After memset (256MB dirtied)", base, after_dirty);
    printf("       -> Dirtying throughput: %.2f GB/s (took %.2f ms)\n\n",
           (256.0 / 1024.0) / (touch_ms / 1000.0), touch_ms);

    // Test 3: MADV_DONTNEED on 128MB
    size_t half_sz = alloc_sz / 2;
    t0 = mach_absolute_time();
    int ret = madvise(ptr, half_sz, MADV_DONTNEED);
    t1 = mach_absolute_time();
    double madv_dontneed_ms = (double)(t1 - t0) * timebase.numer / timebase.denom / 1e6;
    MemoryStats after_dontneed = get_memory_stats();
    print_stats("madvise(MADV_DONTNEED, 128MB)", base, after_dontneed);
    printf("       -> ret=%d, latency: %.3f ms (%.2f GB/s reclaimed)\n\n",
           ret, madv_dontneed_ms, (128.0 / 1024.0) / (madv_dontneed_ms / 1000.0));

    // Test 4: MADV_FREE on remaining 128MB
    void *second_half = (char *)ptr + half_sz;
    t0 = mach_absolute_time();
    ret = madvise(second_half, half_sz, MADV_FREE);
    t1 = mach_absolute_time();
    double madv_free_ms = (double)(t1 - t0) * timebase.numer / timebase.denom / 1e6;
    MemoryStats after_free = get_memory_stats();
    print_stats("madvise(MADV_FREE, 128MB)", base, after_free);
    printf("       -> ret=%d, latency: %.3f ms\n\n", ret, madv_free_ms);

    // Test 5: MADV_FREE_REUSABLE on newly allocated & dirtied 128MB
    void *ptr2 = mmap(NULL, half_sz, PROT_READ | PROT_WRITE, MAP_ANON | MAP_PRIVATE, -1, 0);
    memset(ptr2, 0xBB, half_sz);
    MemoryStats after_ptr2 = get_memory_stats();
    print_stats("After new 128MB dirty", base, after_ptr2);

    t0 = mach_absolute_time();
    ret = madvise(ptr2, half_sz, MADV_FREE_REUSABLE);
    t1 = mach_absolute_time();
    double madv_reusable_ms = (double)(t1 - t0) * timebase.numer / timebase.denom / 1e6;
    MemoryStats after_reusable = get_memory_stats();
    print_stats("madvise(MADV_FREE_REUSABLE, 128MB)", base, after_reusable);
    printf("       -> ret=%d, latency: %.3f ms\n\n", ret, madv_reusable_ms);

    // Test 6: Reusing with MADV_FREE_REUSE
    t0 = mach_absolute_time();
    ret = madvise(ptr2, half_sz, MADV_FREE_REUSE);
    t1 = mach_absolute_time();
    double madv_reuse_ms = (double)(t1 - t0) * timebase.numer / timebase.denom / 1e6;
    MemoryStats after_reuse = get_memory_stats();
    print_stats("madvise(MADV_FREE_REUSE, 128MB)", base, after_reuse);
    printf("       -> ret=%d, latency: %.3f ms\n\n", ret, madv_reuse_ms);

    // Test 7: mach_vm_deallocate vs munmap
    t0 = mach_absolute_time();
    munmap(ptr, alloc_sz);
    munmap(ptr2, half_sz);
    t1 = mach_absolute_time();
    MemoryStats after_munmap = get_memory_stats();
    print_stats("After munmap() all", base, after_munmap);

    printf("\n================================================================================\n");
    printf("Sub-page (Misaligned) Granularity Test\n");
    printf("What happens if unmapping 4KB when host page size is 16KB?\n");
    printf("================================================================================\n");
    void *sub_ptr = mmap(NULL, 16384 * 4, PROT_READ | PROT_WRITE, MAP_ANON | MAP_PRIVATE, -1, 0);
    memset(sub_ptr, 0xCC, 16384 * 4);
    
    // Attempt madvise with 4096 bytes (non-host page size aligned or single 4K chunk)
    int sub_ret = madvise(sub_ptr, 4096, MADV_DONTNEED);
    printf("madvise(MADV_DONTNEED, 4096 bytes): return code = %d (errno if -1: %s)\n",
           sub_ret, sub_ret != 0 ? strerror(errno) : "Success");
    
    // What if pointer is offset by 4096?
    void *unaligned_ptr = (char *)sub_ptr + 4096;
    int unalign_ret = madvise(unaligned_ptr, 16384, MADV_DONTNEED);
    printf("madvise(MADV_DONTNEED, unaligned ptr +4096, 16KB): return code = %d (errno: %s)\n",
           unalign_ret, unalign_ret != 0 ? strerror(errno) : "Success");

    munmap(sub_ptr, 16384 * 4);
    return 0;
}
