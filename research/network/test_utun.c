#include <stdio.h>
#include <string.h>
#include <unistd.h>
#include <sys/socket.h>
#include <sys/ioctl.h>
#include <sys/sys_domain.h>
#include <sys/kern_control.h>
#include <net/if_utun.h>
#include <errno.h>

int main() {
    int fd = socket(PF_SYSTEM, SOCK_DGRAM, SYSPROTO_CONTROL);
    if (fd < 0) {
        printf("socket(PF_SYSTEM, SOCK_DGRAM, SYSPROTO_CONTROL) failed: %s (%d)\n", strerror(errno), errno);
        return 1;
    }
    
    struct ctl_info ctl;
    memset(&ctl, 0, sizeof(ctl));
    strncpy(ctl.ctl_name, UTUN_CONTROL_NAME, sizeof(ctl.ctl_name));
    if (ioctl(fd, CTLIOCGINFO, &ctl) < 0) {
        printf("ioctl CTLIOCGINFO failed: %s (%d)\n", strerror(errno), errno);
        close(fd);
        return 1;
    }
    
    struct sockaddr_ctl sc;
    memset(&sc, 0, sizeof(sc));
    sc.sc_len = sizeof(sc);
    sc.sc_family = AF_SYSTEM;
    sc.ss_sysaddr = AF_SYS_CONTROL;
    sc.sc_id = ctl.ctl_id;
    sc.sc_unit = 0; // Allocate dynamically
    
    if (connect(fd, (struct sockaddr *)&sc, sizeof(sc)) < 0) {
        printf("connect(utun) failed: %s (%d)\n", strerror(errno), errno);
        close(fd);
        return 1;
    }
    
    char ifname[64];
    socklen_t len = sizeof(ifname);
    if (getsockopt(fd, SYSPROTO_CONTROL, UTUN_OPT_IFNAME, ifname, &len) == 0) {
        printf("SUCCESS! Created unprivileged utun interface: %s\n", ifname);
    } else {
        printf("getsockopt(UTUN_OPT_IFNAME) failed: %s\n", strerror(errno));
    }
    close(fd);
    return 0;
}
