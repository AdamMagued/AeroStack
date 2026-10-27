import SwiftUI

// MARK: - Main Application Window

public struct OrbMainView: View {
    @StateObject private var store = OrbDaemonStore()
    @State private var selectedTab: SidebarTab? = .machines
    @State private var showCreateMachineSheet = false
    
    public enum SidebarTab: String, CaseIterable, Identifiable {
        case machines = "Machines"
        case containers = "Containers"
        case monitor = "Activity Monitor"
        case settings = "Settings"
        
        public var id: String { rawValue }
        
        var icon: String {
            switch self {
            case .machines: return "desktopcomputer"
            case .containers: return "shippingbox.fill"
            case .monitor: return "chart.xyaxis.line"
            case .settings: return "gearshape.fill"
            }
        }
    }
    
    public var body: some View {
        NavigationSplitView {
            List(SidebarTab.allCases, selection: $selectedTab) { tab in
                NavigationLink(value: tab) {
                    Label(tab.rawValue, systemImage: tab.icon)
                }
            }
            .listStyle(.sidebar)
            .navigationTitle("OrbStack Free")
        } detail: {
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
                Text("Select a view from the sidebar")
            }
        }
        .sheet(isPresented: $showCreateMachineSheet) {
            CreateMachineSheet(store: store)
        }
    }
}

// MARK: - Machines View

struct MachinesView: View {
    @ObservedObject var store: OrbDaemonStore
    @Binding var showCreateSheet: Bool
    
    var body: some View {
        VStack(spacing: 0) {
            // Header bar
            HStack {
                Text("Linux Machines")
                    .font(.title2)
                    .fontWeight(.bold)
                Spacer()
                Button(action: { showCreateSheet = true }) {
                    Label("New Machine", systemImage: "plus")
                }
                .buttonStyle(.borderedProminent)
            }
            .padding()
            
            Divider()
            
            if store.machines.isEmpty {
                VStack(spacing: 12) {
                    Image(systemName: "desktopcomputer.trianglebadge.exclamationmark")
                        .font(.system(size: 48))
                        .foregroundColor(.secondary)
                    Text("No Linux Machines Found")
                        .font(.headline)
                    Text("Create a machine with Ubuntu, Debian, Alpine, Arch, or Fedora in seconds.")
                        .foregroundColor(.secondary)
                    Button("Create Machine") {
                        showCreateSheet = true
                    }
                    .buttonStyle(.borderedProminent)
                }
                .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else {
                List(store.machines) { machine in
                    HStack(spacing: 16) {
                        Circle()
                            .fill(machine.isRunning ? Color.green : Color.gray.opacity(0.5))
                            .frame(width: 10, height: 10)
                        
                        VStack(alignment: .leading, spacing: 4) {
                            Text(machine.name)
                                .font(.headline)
                            Text("\(machine.distro) \(machine.version) • \(machine.arch) • \(machine.size)")
                                .font(.caption)
                                .foregroundColor(.secondary)
                        }
                        
                        Spacer()
                        
                        if machine.isRunning {
                            Text(machine.ip_address)
                                .font(.system(.caption, design: .monospaced))
                                .padding(4)
                                .background(Color.secondary.opacity(0.1))
                                .cornerRadius(4)
                            
                            Button(action: { store.stopMachine(name: machine.name) }) {
                                Image(systemName: "stop.fill")
                            }
                            .help("Stop Machine")
                        } else {
                            Button(action: { store.startMachine(name: machine.name) }) {
                                Image(systemName: "play.fill")
                            }
                            .help("Start Machine")
                        }
                        
                        Button(action: { /* open terminal */ }) {
                            Image(systemName: "terminal")
                        }
                        .help("Launch PTY Terminal")
                    }
                    .padding(.vertical, 6)
                }
            }
        }
    }
}

// MARK: - Containers View

struct ContainersView: View {
    @ObservedObject var store: OrbDaemonStore
    
    var body: some View {
        VStack(spacing: 0) {
            HStack {
                Text("Docker Containers")
                    .font(.title2)
                    .fontWeight(.bold)
                Spacer()
                Button(action: { store.fetchContainers() }) {
                    Image(systemName: "arrow.clockwise")
                }
            }
            .padding()
            
            Divider()
            
            List(store.containers) { c in
                VStack(alignment: .leading, spacing: 8) {
                    HStack {
                        Circle()
                            .fill(c.state == "running" ? Color.green : Color.gray)
                            .frame(width: 8, height: 8)
                        Text(c.displayName)
                            .font(.headline)
                        Text(c.image)
                            .font(.subheadline)
                            .foregroundColor(.secondary)
                        Spacer()
                        Text(c.status)
                            .font(.caption)
                            .foregroundColor(.secondary)
                    }
                    
                    if !c.mounts.isEmpty {
                        HStack {
                            Text("VirtioFS Mounts:")
                                .font(.caption)
                                .fontWeight(.medium)
                            ForEach(Array(c.mounts.keys), id: \.self) { target in
                                Text("\(c.mounts[target] ?? "") ➔ \(target)")
                                    .font(.system(.caption2, design: .monospaced))
                                    .padding(3)
                                    .background(Color.blue.opacity(0.1))
                                    .cornerRadius(4)
                            }
                        }
                    }
                }
                .padding(.vertical, 4)
            }
        }
    }
}

// MARK: - Activity Monitor View

struct ActivityMonitorView: View {
    @ObservedObject var store: OrbDaemonStore
    
    var body: some View {
        VStack(alignment: .leading, spacing: 20) {
            Text("Resource Performance Monitor")
                .font(.title2)
                .fontWeight(.bold)
                .padding(.top)
            
            if let m = store.metrics {
                LazyVGrid(columns: [GridItem(.flexible()), GridItem(.flexible())], spacing: 16) {
                    MetricCard(title: "Host CPU Usage", value: String(format: "%.1f%%", m.host_cpu_percent), icon: "cpu")
                    MetricCard(title: "Guest VM CPU", value: String(format: "%.1f%%", m.guest_cpu_percent), icon: "bolt.fill")
                    MetricCard(title: "Memory In-Use", value: "\(m.memory_used_mb) MB / \(m.memory_total_mb) MB", icon: "memorychip")
                    MetricCard(title: "Balloon Memory Freed", value: "\(m.balloon_freed_mb) MB", icon: "arrow.down.forward.and.arrow.up.backward")
                    MetricCard(title: "VirtioFS Ops/Sec", value: "\(m.virtiofs_ops_per_sec) ops/s", icon: "externaldrive.fill")
                    MetricCard(title: "VirtioFS Cache Hit Rate", value: String(format: "%.1f%%", m.virtiofs_cache_hit_rate), icon: "speedometer")
                }
            } else {
                ProgressView("Connecting to Daemon...")
            }
            
            Spacer()
        }
        .padding()
    }
}

struct MetricCard: View {
    let title: String
    let value: String
    let icon: String
    
    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack {
                Image(systemName: icon)
                    .foregroundColor(.accentColor)
                Text(title)
                    .font(.subheadline)
                    .foregroundColor(.secondary)
            }
            Text(value)
                .font(.title3)
                .fontWeight(.semibold)
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding()
        .background(Color(NSColor.controlBackgroundColor))
        .cornerRadius(8)
    }
}

// MARK: - Settings View

struct SettingsView: View {
    @ObservedObject var store: OrbDaemonStore
    
    var body: some View {
        Form {
            Section("Compute Resources") {
                Slider(value: Binding(
                    get: { Double(store.settings?.max_cpus ?? 4) },
                    set: { store.settings?.max_cpus = Int($0) }
                ), in: 1...12, step: 1) {
                    Text("CPU Limit: \(store.settings?.max_cpus ?? 4) Cores")
                }
                
                Slider(value: Binding(
                    get: { Double(store.settings?.max_memory_mb ?? 4096) / 1024.0 },
                    set: { store.settings?.max_memory_mb = Int($0 * 1024) }
                ), in: 2...32, step: 1) {
                    Text("Memory Limit: \(Int(Double(store.settings?.max_memory_mb ?? 4096)/1024.0)) GB")
                }
            }
            
            Section("Virtualization Features") {
                Toggle("Rosetta 2 x86_64 Emulation in Linux", isOn: Binding(
                    get: { store.settings?.rosetta_x86 ?? true },
                    set: { store.settings?.rosetta_x86 = $0 }
                ))
                Toggle("Fast VirtioFS Host Sharing", isOn: Binding(
                    get: { store.settings?.fast_virtiofs ?? true },
                    set: { store.settings?.fast_virtiofs = $0 }
                ))
                Toggle("Dynamic Battery Saver Ballooning", isOn: Binding(
                    get: { store.settings?.battery_saver ?? false },
                    set: { store.settings?.battery_saver = $0 }
                ))
            }
        }
        .padding()
    }
}

// MARK: - Create Machine Sheet

struct CreateMachineSheet: View {
    @ObservedObject var store: OrbDaemonStore
    @Environment(\.dismiss) private var dismiss
    
    @State private var distro: String = "ubuntu"
    @State private var machineName: String = "dev"
    @State private var cpus: Int = 4
    @State private var memoryGB: Int = 4
    
    let distros = ["ubuntu", "debian", "alpine", "arch", "fedora"]
    
    var body: some View {
        VStack(spacing: 20) {
            Text("Create Linux Machine")
                .font(.headline)
            
            Picker("Distribution", selection: $distro) {
                ForEach(distros, id: \.self) { d in
                    Text(d.capitalized).tag(d)
                }
            }
            .pickerStyle(.segmented)
            
            TextField("Machine Name", text: $machineName)
                .textFieldStyle(.roundedBorder)
            
            Stepper("CPU Cores: \(cpus)", value: $cpus, in: 1...8)
            Stepper("Memory: \(memoryGB) GB", value: $memoryGB, in: 1...16)
            
            HStack {
                Button("Cancel") { dismiss() }
                Spacer()
                Button("Create (< 1s CoW)") {
                    store.createMachine(distro: distro, name: machineName, cpus: cpus, memoryMB: memoryGB * 1024)
                    dismiss()
                }
                .buttonStyle(.borderedProminent)
            }
        }
        .padding(24)
        .frame(width: 400)
    }
}
