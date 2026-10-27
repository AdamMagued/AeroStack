#ifndef VSOCK_MEM_PROTOCOL_H
#define VSOCK_MEM_PROTOCOL_H

#include <stdint.h>

#define VSOCK_MEM_PORT 1024
#define PROTOCOL_MAGIC 0x4D454D31  // "MEM1"

// Message Types
typedef enum {
    MSG_TELEMETRY_REPORT  = 1, // Guest -> Host: Current memory & PSI metrics
    MSG_PRESSURE_ALERT    = 2, // Guest -> Host: Urgent memory stall / OOM hazard
    MSG_COMPACT_COMMAND   = 3, // Host -> Guest: Request memory compaction / drop caches
    MSG_SET_TARGET_ACK    = 4, // Host -> Guest: Notification of new memory ceiling
    MSG_HEARTBEAT         = 5, // Bidirectional ping/pong
} MessageType;

#pragma pack(push, 1)

// Guest -> Host Telemetry Frame (32 bytes)
typedef struct {
    uint32_t magic;            // PROTOCOL_MAGIC
    uint8_t  msg_type;         // MessageType
    uint8_t  reserved[3];
    uint64_t total_ram_bytes;  // Total physical RAM visible to guest
    uint64_t free_ram_bytes;   // Free pages in buddy allocator
    uint64_t avail_ram_bytes;  // MemAvailable from /proc/meminfo
    uint32_t psi_some_avg10;   // Memory PSI some 10s avg * 100 (e.g. 1.25% = 125)
    uint32_t psi_full_avg10;   // Memory PSI full 10s avg * 100
} GuestTelemetryFrame;

// Host -> Guest Control Frame (16 bytes)
typedef struct {
    uint32_t magic;            // PROTOCOL_MAGIC
    uint8_t  msg_type;         // MessageType
    uint8_t  flags;            // 0x1: compact_memory, 0x2: drop_caches, 0x4: trim_slab
    uint16_t reserved;
    uint64_t target_ram_bytes; // New target memory size
} HostControlFrame;

#pragma pack(pop)

#endif // VSOCK_MEM_PROTOCOL_H
