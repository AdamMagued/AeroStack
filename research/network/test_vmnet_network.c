#include <stdio.h>
#include <vmnet/vmnet.h>

int main() {
    vmnet_return_t status = 0;
    vmnet_network_configuration_ref config = vmnet_network_configuration_create(VMNET_SHARED_MODE, &status);
    printf("vmnet_network_configuration_create: config=%p, status=%d\n", config, status);
    
    if (config) {
        vmnet_network_ref net = vmnet_network_create(config, &status);
        printf("vmnet_network_create: net=%p, status=%d\n", net, status);
    }
    return 0;
}
