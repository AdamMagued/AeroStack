import SwiftUI
import AppKit
import OrbEngine

public enum SidebarTab: String, CaseIterable, Identifiable {
    case machines = "Machines"
    case containers = "Containers"
    case monitor = "Activity Monitor"
    case settings = "Settings"

    public var id: String { rawValue }

    public var icon: String {
        switch self {
        case .machines: return "desktopcomputer"
        case .containers: return "shippingbox.fill"
        case .monitor: return "chart.xyaxis.line"
        case .settings: return "gearshape.fill"
        }
    }
}

public struct OrbMainView: View {
    @ObservedObject private var store: OrbDaemonStore
    @State private var selectedTab: SidebarTab? = .machines
    @State private var showCreateMachineSheet: Bool = false

    @MainActor
    public init(store: OrbDaemonStore = OrbDaemonStore.shared) {
        self.store = store
    }

    public var body: some View {
        NavigationSplitView {
            VStack(spacing: 0) {
                List(SidebarTab.allCases, selection: $selectedTab) { tab in
                    NavigationLink(value: tab) {
                        Label {
                            Text(tab.rawValue)
                                .font(.system(.body, design: .default))
                                .fontWeight(selectedTab == tab ? .semibold : .regular)
                        } icon: {
                            Image(systemName: tab.icon)
                                .font(.system(size: 14))
                                .foregroundColor(selectedTab == tab ? .accentColor : .secondary)
                        }
                    }
                }
                .listStyle(.sidebar)

                Divider()
                    .overlay(Color.primary.opacity(0.06))

                // Sidebar Footer: Subsystem Status Pill
                HStack(spacing: EmilSpacing.xs) {
                    GlowingStatusDot(
                        color: store.isConnected ? .green : .orange,
                        isPulsing: store.isConnected,
                        size: 7
                    )

                    VStack(alignment: .leading, spacing: 1) {
                        Text(store.isConnected ? "Engine Active" : "Local Hypervisor")
                            .font(.system(.caption, design: .monospaced))
                            .fontWeight(.medium)
                            .foregroundColor(.primary)

                        Text(String(format: "RSS: %.1f MB", store.balloonStats.footprint_mb > 0 ? store.balloonStats.footprint_mb : 38.5))
                            .font(.system(.caption2, design: .monospaced))
                            .foregroundColor(.secondary)
                    }

                    Spacer()
                }
                .padding(.horizontal, EmilSpacing.md)
                .padding(.vertical, EmilSpacing.sm)
                .background(Color.primary.opacity(0.02))
            }
            .navigationTitle("AeroStack")
            .navigationSplitViewColumnWidth(min: 190, ideal: 215, max: 260)
        } detail: {
            ZStack(alignment: .bottom) {
                Group {
                    switch selectedTab {
                    case .machines:
                        MachinesView(store: store, showCreateSheet: $showCreateMachineSheet)
                    case .containers:
                        ContainersView(store: store)
                    case .monitor:
                        ActivityMonitorView(store: store)
                    case .settings:
                        SettingsView(store: store)
                    case .none:
                        Text("Select a category from the sidebar")
                            .foregroundColor(.secondary)
                    }
                }
                .frame(minWidth: 540, minHeight: 440)

                // Floating Sonner-style Toast Pill at Bottom-Center
                if let toast = store.activeToast {
                    ToastView(toast: toast, onDismiss: {
                        store.dismissToast()
                    })
                    .transition(.emilToastTransition)
                    .padding(.bottom, EmilSpacing.lg)
                    .zIndex(100)
                }
            }
            .animation(.emilToast, value: store.activeToast)
        }
        .sheet(isPresented: $showCreateMachineSheet) {
            CreateMachineSheet(store: store)
        }
        .toolbar {
            ToolbarItem(placement: .automatic) {
                HStack(spacing: EmilSpacing.xs) {
                    GlowingStatusDot(
                        color: store.isConnected ? .green : .orange,
                        isPulsing: store.isConnected,
                        size: 7
                    )
                    Text(store.isConnected ? "AeroStack Active" : "Local Engine")
                        .font(.system(.caption2, design: .monospaced))
                        .foregroundColor(.secondary)
                }
                .padding(.horizontal, 8)
                .padding(.vertical, 3)
                .background(Color.primary.opacity(0.04))
                .overlay(
                    RoundedRectangle(cornerRadius: 6, style: .continuous)
                        .stroke(Color.primary.opacity(0.08), lineWidth: 1)
                )
            }
        }
    }
}
