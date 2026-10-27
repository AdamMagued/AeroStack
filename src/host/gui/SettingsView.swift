import SwiftUI
import OrbEngine

public struct SettingsView: View {
    @ObservedObject var store: OrbDaemonStore

    @State private var maxCPUs: Int = 10
    @State private var maxMemoryMB: Int = 8192
    @State private var maxDiskGB: Int = 64
    @State private var rosettaX86: Bool = true
    @State private var fastVirtioFS: Bool = true
    @State private var batterySaver: Bool = false
    @State private var autoStart: Bool = true
    @State private var dnsDomain: String = "orb.local"
    @State private var storageLocation: String = "/Volumes/AdamSSD/antigravity_workspace/orbstack_free/data"
    @State private var saveSuccess: Bool = false

    public var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 24) {
                // Header
                VStack(alignment: .leading, spacing: 4) {
                    Text("Settings & Resource Allocation")
                        .font(.title2)
                        .fontWeight(.bold)
                    Text("Fine-tune Apple Silicon hypervisor limits and guest integrations")
                        .font(.subheadline)
                        .foregroundColor(.secondary)
                }

                Divider()

                // Resources
                VStack(alignment: .leading, spacing: 16) {
                    Text("Hardware Resources")
                        .font(.headline)

                    HStack {
                        Text("CPU Limit:")
                            .frame(width: 140, alignment: .leading)
                        Stepper("\(maxCPUs) vCPUs", value: $maxCPUs, in: 1...16)
                    }

                    HStack {
                        Text("Memory Limit:")
                            .frame(width: 140, alignment: .leading)
                        Stepper("\(maxMemoryMB / 1024) GB (\(maxMemoryMB) MB)", value: $maxMemoryMB, in: 1024...32768, step: 1024)
                    }

                    HStack {
                        Text("Virtual Disk Size:")
                            .frame(width: 140, alignment: .leading)
                        Stepper("\(maxDiskGB) GB", value: $maxDiskGB, in: 16...512, step: 16)
                    }
                }
                .padding()
                .background(Color.secondary.opacity(0.05))
                .cornerRadius(10)

                // Engine Features
                VStack(alignment: .leading, spacing: 16) {
                    Text("Engine Optimizations")
                        .font(.headline)

                    Toggle("Rosetta 2 x86 Emulation for Linux", isOn: $rosettaX86)
                        .help("Enable high-performance x86_64 translation on Apple Silicon")

                    Toggle("Fast VirtioFS with FSEvents Sync (< 1ms)", isOn: $fastVirtioFS)
                        .help("Eliminates file polling for vite, nextjs, and node file watchers")

                    Toggle("Aggressive Battery Saver", isOn: $batterySaver)
                        .help("Drops memory balloon immediately to 48MB when on battery power")

                    Toggle("Auto-start VM Daemon on macOS Login", isOn: $autoStart)
                }
                .padding()
                .background(Color.secondary.opacity(0.05))
                .cornerRadius(10)

                // Storage & Networking
                VStack(alignment: .leading, spacing: 16) {
                    Text("Storage & Networking")
                        .font(.headline)

                    HStack {
                        Text("Local DNS:")
                            .frame(width: 140, alignment: .leading)
                        TextField("Domain", text: $dnsDomain)
                            .textFieldStyle(.roundedBorder)
                    }

                    HStack {
                        Text("Storage Path:")
                            .frame(width: 140, alignment: .leading)
                        TextField("Path", text: $storageLocation)
                            .textFieldStyle(.roundedBorder)
                    }
                }
                .padding()
                .background(Color.secondary.opacity(0.05))
                .cornerRadius(10)

                HStack {
                    if saveSuccess {
                        Text("✓ Settings saved successfully")
                            .font(.caption)
                            .foregroundColor(.green)
                    }
                    Spacer()
                    Button("Apply & Save Settings") {
                        let newSettings = OrbSettings(
                            max_cpus: maxCPUs,
                            max_memory_mb: maxMemoryMB,
                            max_disk_gb: maxDiskGB,
                            rosetta_x86: rosettaX86,
                            fast_virtiofs: fastVirtioFS,
                            battery_saver: batterySaver,
                            auto_start: autoStart,
                            dns_domain: dnsDomain,
                            storage_location: storageLocation
                        )
                        store.updateSettings(newSettings)
                        saveSuccess = true
                        DispatchQueue.main.asyncAfter(deadline: .now() + 2.5) {
                            saveSuccess = false
                        }
                    }
                    .buttonStyle(.borderedProminent)
                }
            }
            .padding(20)
        }
        .onAppear {
            syncFromStore()
        }
    }

    private func syncFromStore() {
        let s = store.settings
        self.maxCPUs = s.max_cpus
        self.maxMemoryMB = s.max_memory_mb
        self.maxDiskGB = s.max_disk_gb
        self.rosettaX86 = s.rosetta_x86
        self.fastVirtioFS = s.fast_virtiofs
        self.batterySaver = s.battery_saver
        self.autoStart = s.auto_start
        self.dnsDomain = s.dns_domain
        self.storageLocation = s.storage_location
    }
}
