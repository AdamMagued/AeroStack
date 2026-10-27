#include <stdio.h>
#include <vmnet/vmnet.h>

int main() {
    vmnet_return_t status;
    printf("Testing vmnet unprivileged...\n");
    xpc_object_t desc = xpc_dictionary_create(NULL, NULL, 0);
    xpc_dictionary_set_uint64(desc, vmnet_operation_mode_key, VMNET_SHARED_MODE);
    
    interface_ref iface = vmnet_start_interface(desc, dispatch_get_main_queue(), ^(vmnet_return_t status, xpc_object_t interface_param) {
        printf("vmnet_start_interface callback status: %d\n", status);
    });
    
    if (iface == NULL) {
        printf("vmnet_start_interface returned NULL (expected without root/entitlement)\n");
    } else {
        printf("vmnet_start_interface returned interface ref: %p\n", iface);
    }

    dispatch_after(dispatch_time(DISPATCH_TIME_NOW, 1 * NSEC_PER_SEC), dispatch_get_main_queue(), ^{
        printf("Done waiting for vmnet callback.\n");
        exit(0);
    });

    dispatch_main();
    return 0;
}
