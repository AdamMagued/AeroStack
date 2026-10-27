import SwiftUI
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
    @StateObject private var store = OrbDaemonStore()
    @State private var selectedTab: SidebarTab? = .machines
    @State private var showCreateMachineSheet: Bool = false

    public init() {}

    public var body: some View {
        NavigationSplitView {
            List(SidebarTab.allCases, selection: $selectedTab) { tab in
                NavigationLink(value: tab) {
                    Label(tab.rawValue, systemImage: tab.icon)
                }
            }
            .listStyle(.sidebar)
            .navigationTitle("OrbStack Free")
            .navigationSplitViewColumnWidth(min: 180, ideal: 200, max: 240)
        } detail: {
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
            .frame(minWidth: 500, minHeight: 400)
        }
        .sheet(isPresented: $showCreateMachineSheet) {
            CreateMachineSheet(store: store)
        }
        .toolbar {
            ToolbarItem(placement: .automatic) {
                HStack(spacing: 6) {
                    Circle()
                        .fill(store.isConnected ? Color.green : Color.orange)
                        .frame(width: 8, height: 8)
                    Text(store.isConnected ? "Engine Connected" : "Local Mode")
                        .font(.caption2)
                        .foregroundColor(.secondary)
                }
            }
        }
    }
}
