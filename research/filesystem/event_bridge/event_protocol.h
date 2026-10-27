#ifndef ORB_EVENT_PROTOCOL_H
#define ORB_EVENT_PROTOCOL_H

#include <stdint.h>

#define ORB_FS_MAGIC 0x4F524246  /* "ORBF" */
#define ORB_FS_VERSION 1

/* Protocol Opcodes */
#define ORB_OP_EVENT        1
#define ORB_OP_PING         2
#define ORB_OP_PONG         3
#define ORB_OP_RESCAN       4
#define ORB_OP_ACK          5

/* Unified Inotify Event Bitmask (matches Linux inotify.h) */
#define ORB_IN_ACCESS        0x00000001
#define ORB_IN_MODIFY        0x00000002
#define ORB_IN_ATTRIB        0x00000004
#define ORB_IN_CLOSE_WRITE   0x00000008
#define ORB_IN_CLOSE_NOWRITE 0x00000010
#define ORB_IN_OPEN          0x00000020
#define ORB_IN_MOVED_FROM    0x00000040
#define ORB_IN_MOVED_TO      0x00000080
#define ORB_IN_CREATE        0x00000100
#define ORB_IN_DELETE        0x00000200
#define ORB_IN_DELETE_SELF   0x00000400
#define ORB_IN_MOVE_SELF     0x00000800
#define ORB_IN_ISDIR         0x40000000

#define ORB_MAX_PATH_LEN     4096

#pragma pack(push, 1)
typedef struct {
    uint32_t magic;          /* ORB_FS_MAGIC */
    uint16_t version;        /* ORB_FS_VERSION */
    uint16_t opcode;         /* ORB_OP_* */
    uint32_t inotify_mask;   /* ORB_IN_* bitmask */
    uint64_t seq;            /* Monotonic sequence number */
    uint64_t timestamp_ns;   /* Host nanosecond timestamp */
    uint32_t path_len;       /* Length of relative path (excluding trailing null) */
} orb_event_header_t;
#pragma pack(pop)

#endif /* ORB_EVENT_PROTOCOL_H */
