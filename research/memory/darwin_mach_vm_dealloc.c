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

static void print_stats(const char *label) {
    mach_msg_type_number_t count;
    mach_task_basic_info_data_t basic_info;
    task_vm_info_data_t vm_info;
    
    count = MACH_TASK_BASIC_INFO_COUNT;
    task_info(mach_task_self(), MACH_TASK_BASIC_INFO, (task_info_t)&basic_info, &count);
    
    count = TASK_VM_INFO_COUNT;
    task_info(mach_task_self(), TASK_VM_INFO, (task_info_t)&vm_info, &count);

    printf("%-35s | Resident: %8.2f MB | Footprint: %8.2f MB | Purgeable: %8.2f MB | Reusable: %8.2f MB\n",
           label,
           (double)basic_info.resident_size / (1024.0 * 1024.0),
           (double)vm_info.phys_footprint / (1024.0 * 1024.0),
           (double)vm_info.purgeable_volatile_pmap / (1024.0 * 1024.0),
           (double)vm_info.reusable / (1024.0 * 1024.0));
}

int main(void) {
    printf("================================================================================\n");
    printf("Darwin Mach VM Deallocation & Replacement Deep Test\n");
    printf("================================================================================\n");

    print_stats("Baseline");

    size_t sz = 256 * 1024 * 1024;
    void *ptr = mmap(NULL, sz, PROT_READ | PROT_WRITE, MAP_ANON | MAP_PRIVATE, -1, 0);
    memset(ptr, 0xFF, sz);
    print_stats("Dirtied 256MB");

    // Test A: mach_vm_deallocate 128MB
    kern_return_t kr = mach_vm_deallocate(mach_task_self(), (mach_vm_address_t)ptr, 128 * 1024 * 1024);
    printf("mach_vm_deallocate(128MB): kr=%d (%s)\n", kr, mach_error_string(kr));
    print_stats("After mach_vm_deallocate 128MB");

    // Test B: Remap that 128MB hole with MAP_FIXED
    void *remap = mmap(ptr, 128 * 1024 * 1024, PROT_READ | PROT_WRITE, MAP_ANON | MAP_PRIVATE | MAP_FIXED, -1, 0);
    printf("mmap(MAP_FIXED 128MB): remap=%p (ptr was %p)\n", remap, ptr);
    print_stats("After MAP_FIXED (untouched)");

    // Test C: Re-dirty the remapped 128MB
    memset(remap, 0x55, 128 * 1024 * 1024);
    print_stats("After re-dirtying 128MB");

    // Test D: Test vm_read_overwrite / mincore
    char vec[256];
    int mc = mincore(remap, 16384 * 256, vec);
    printf("mincore: mc=%d, vec[0]=0x%x, vec[1]=0x%x\n", mc, (unsigned char)vec[0], (unsigned char)vec[1]);

    // Test E: Test madvise(MADV_ZERO_WIRED_PAGES) if any, or MADV_DONTNEED on MAP_FIXED
    // In Darwin, how does Virtualization.framework manage guest RAM?
    munmap(ptr, sz);
    print_stats("After munmap all");

    return 0;
}
