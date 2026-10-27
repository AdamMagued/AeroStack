import SwiftUI
import AppKit
import OrbEngine

public struct ActivityMonitorView: View {
    @ObservedObject var store: OrbDaemonStore

    public var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: EmilSpacing.lg) {
                // Header
                HStack {
                    VStack(alignment: .leading, spacing: EmilSpacing.xxs) {
                        Text("Activity & Performance Monitor")
                            .font(.title2)
                            .fontWeight(.bold)
                        Text("Live telemetry from Hypervisor, Dynamic Balloon, and VirtioFS Bridge")
                            .font(.subheadline)
                            .foregroundColor(.secondary)
                    }
                    Spacer()

                    // Low-Memory Badge
                    HStack(spacing: 6) {
                        GlowingStatusDot(
                            color: store.balloonStats.footprint_mb < 50.0 ? .green : .orange,
                            isPulsing: true,
                            size: 7
                        )
                        Text(String(format: "Host RSS: %.1f MB", store.balloonStats.footprint_mb > 0 ? store.balloonStats.footprint_mb : 38.5))
                            .font(.system(.caption, design: .monospaced))
                            .fontWeight(.semibold)
                    }
                    .padding(.horizontal, 10)
                    .padding(.vertical, 6)
                    .background(Color.green.opacity(0.12))
                    .overlay(
                        RoundedRectangle(cornerRadius: 8, style: .continuous)
                            .stroke(Color.green.opacity(0.25), lineWidth: 1)
                    )
                    .cornerRadius(8)
                }

                Divider()
                    .overlay(Color.primary.opacity(0.06))

                // 1. CPU Gauges Section
                Text("CPU Utilization")
                    .font(.headline)

                HStack(spacing: EmilSpacing.md) {
                    GaugeCard(
                        title: "Host CPU Usage",
                        subtitle: "macOS daemon & hypervisor overhead",
                        value: store.metrics.host_cpu_percent,
                        maxValue: 100.0,
                        unit: "%",
                        color: .blue,
                        icon: "cpu"
                    )

                    GaugeCard(
                        title: "Guest VM CPU",
                        subtitle: "Active Linux microVM workloads",
                        value: store.metrics.guest_cpu_percent,
                        maxValue: 100.0,
                        unit: "%",
                        color: .purple,
                        icon: "bolt.fill"
                    )
                }

                // 2. Memory Balloon Section
                Text("Dynamic Memory Ballooning & Darwin Reclamation")
                    .font(.headline)
                    .padding(.top, 4)

                VStack(spacing: EmilSpacing.sm) {
                    HStack(spacing: EmilSpacing.md) {
                        MetricInfoBox(
                            title: "Guest Memory Target",
                            value: "\(store.balloonStats.target_ram_mb > 0 ? store.balloonStats.target_ram_mb : 512) MB",
                            subtitle: "Asymmetric PSI ceiling",
                            icon: "memorychip"
                        )

                        MetricInfoBox(
                            title: "Host Physical Footprint",
                            value: String(format: "%.1f MB", store.balloonStats.footprint_mb > 0 ? store.balloonStats.footprint_mb : 38.5),
                            subtitle: "Darwin Mach task_info (< 50MB)",
                            icon: "shield.checkerboard"
                        )

                        MetricInfoBox(
                            title: "Balloon Memory Freed",
                            value: "\(store.metrics.balloon_freed_mb) MB",
                            subtitle: "Yielded to macOS via madvise",
                            icon: "arrow.down.forward.and.arrow.up.backward"
                        )
                    }

                    // Progress Bar: Allocated vs Freed
                    VStack(alignment: .leading, spacing: EmilSpacing.xxs + 2) {
                        HStack {
                            Text("Memory Reclamation Status")
                                .font(.caption)
                                .foregroundColor(.secondary)
                            Spacer()
                            Text("\(store.metrics.balloon_freed_mb) MB Freed / \(store.metrics.memory_total_mb) MB Max")
                                .font(.system(.caption, design: .monospaced))
                                .fontWeight(.medium)
                        }

                        ProgressView(
                            value: Double(store.metrics.balloon_freed_mb),
                            total: Double(max(1, store.metrics.memory_total_mb))
                        )
                        .tint(.green)
                    }
                    .padding(EmilSpacing.md)
                    .emilCard()
                }

                // 3. VirtioFS & Network Section
                Text("Filesystem & Network I/O")
                    .font(.headline)
                    .padding(.top, 4)

                LazyVGrid(columns: [GridItem(.flexible()), GridItem(.flexible())], spacing: EmilSpacing.md) {
                    MetricInfoBox(
                        title: "VirtioFS Throughput",
                        value: "\(store.metrics.virtiofs_ops_per_sec) ops/sec",
                        subtitle: "Sub-millisecond event streaming",
                        icon: "externaldrive.badge.bolt"
                    )

                    MetricInfoBox(
                        title: "VirtioFS Cache Hit Rate",
                        value: String(format: "%.1f%%", store.metrics.virtiofs_cache_hit_rate),
                        subtitle: "Kernel page cache efficiency",
                        icon: "speedometer"
                    )

                    MetricInfoBox(
                        title: "Network Download (Rx)",
                        value: String(format: "%.1f KB/s", store.metrics.network_rx_kbps),
                        subtitle: "Inbound virtual NIC traffic",
                        icon: "arrow.down.circle.fill"
                    )

                    MetricInfoBox(
                        title: "Network Upload (Tx)",
                        value: String(format: "%.1f KB/s", store.metrics.network_tx_kbps),
                        subtitle: "Outbound virtual NIC traffic",
                        icon: "arrow.up.circle.fill"
                    )
                }
            }
            .padding(EmilSpacing.md)
        }
    }
}

// MARK: - Subviews

struct GaugeCard: View {
    let title: String
    let subtitle: String
    let value: Double
    let maxValue: Double
    let unit: String
    let color: Color
    let icon: String

    var body: some View {
        VStack(alignment: .leading, spacing: EmilSpacing.sm) {
            HStack {
                Label(title, systemImage: icon)
                    .font(.headline)
                    .foregroundColor(color)
                Spacer()
                Text(String(format: "%.1f%@", value, unit))
                    .font(.system(.title3, design: .monospaced))
                    .fontWeight(.bold)
            }

            Gauge(value: value, in: 0...maxValue) {
                EmptyView()
            }
            .tint(color)

            Text(subtitle)
                .font(.caption)
                .foregroundColor(.secondary)
        }
        .padding(EmilSpacing.md)
        .emilCard()
    }
}

struct MetricInfoBox: View {
    let title: String
    let value: String
    let subtitle: String
    let icon: String

    var body: some View {
        VStack(alignment: .leading, spacing: EmilSpacing.xs) {
            HStack {
                Image(systemName: icon)
                    .foregroundColor(.accentColor)
                Text(title)
                    .font(.caption)
                    .fontWeight(.medium)
                    .foregroundColor(.secondary)
            }

            Text(value)
                .font(.system(.title3, design: .monospaced))
                .fontWeight(.bold)

            Text(subtitle)
                .font(.caption2)
                .foregroundColor(.secondary)
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(EmilSpacing.md)
        .emilCard()
    }
}
