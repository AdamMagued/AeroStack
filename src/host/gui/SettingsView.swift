import SwiftUI
import AppKit
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
    @State private var dockerContextSwitched: Bool = false

    public var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: EmilSpacing.lg) {
                // Header
                VStack(alignment: .leading, spacing: EmilSpacing.xxs) {
                    Text("Settings & Diagnostics")
                        .font(.title2)
                        .fontWeight(.bold)
                    Text("Fine-tune Apple Silicon hypervisor limits, Docker CLI integration, and guest subsystems")
                        .font(.subheadline)
                        .foregroundColor(.secondary)
                }

                Divider()
                    .overlay(Color.primary.opacity(0.06))

                // MARK: - 1. Docker CLI Integration Card
                VStack(alignment: .leading, spacing: EmilSpacing.sm) {
                    HStack(spacing: EmilSpacing.xs) {
                        Image(systemName: "shippingbox.fill")
                            .font(.system(size: 16, weight: .semibold))
                            .foregroundColor(.blue)

                        Text("Docker CLI Integration")
                            .font(.headline)

                        Spacer()

                        HStack(spacing: 6) {
                            GlowingStatusDot(color: .green, isPulsing: true, size: 7)
                            Text("AeroStack Socket Active")
                                .font(.system(.caption, design: .monospaced))
                                .fontWeight(.medium)
                                .foregroundColor(.green)
                        }
                        .padding(.horizontal, 8)
                        .padding(.vertical, 3)
                        .background(Color.green.opacity(0.10))
                        .overlay(
                            RoundedRectangle(cornerRadius: 6, style: .continuous)
                                .stroke(Color.green.opacity(0.25), lineWidth: 1)
                        )
                    }

                    Text("Standard Docker CLI commands (docker run, docker compose, docker build) route directly through AeroStack's hypervisor socket proxy.")
                        .font(.caption)
                        .foregroundColor(.secondary)

                    HStack(spacing: EmilSpacing.xs) {
                        Text("Active Socket:")
                            .font(.caption)
                            .fontWeight(.medium)
                            .foregroundColor(.secondary)
                        Text("~/.aerostack/run/docker.sock")
                            .font(.system(.caption, design: .monospaced))
                            .padding(.horizontal, 6)
                            .padding(.vertical, 2)
                            .background(Color.primary.opacity(0.05))
                            .cornerRadius(4)
                    }

                    HStack {
                        Button(action: {
                            store.switchDockerContext()
                            withAnimation(.emilSnappy) {
                                dockerContextSwitched = true
                            }
                        }) {
                            Label("Set as Default Docker Engine", systemImage: "arrow.triangle.swap")
                        }
                        .buttonStyle(EmilProminentButtonStyle(customColor: .blue))

                        if dockerContextSwitched {
                            HStack(spacing: 5) {
                                Image(systemName: "checkmark")
                                    .font(.caption2)
                                    .foregroundColor(.green)
                                Text("Docker Context: aerostack active")
                                    .font(.system(.caption, design: .monospaced))
                                    .foregroundColor(.green)
                            }
                            .padding(.horizontal, 8)
                            .padding(.vertical, 4)
                            .background(Color.green.opacity(0.12))
                            .cornerRadius(6)
                            .transition(.emilScaleEnter)
                        }

                        Spacer()
                    }
                    .padding(.top, EmilSpacing.xxs)
                }
                .padding(EmilSpacing.md)
                .emilCard()

                // MARK: - 2. System Diagnostics (Doctor)
                VStack(alignment: .leading, spacing: EmilSpacing.sm) {
                    HStack(alignment: .center) {
                        VStack(alignment: .leading, spacing: 2) {
                            HStack(spacing: EmilSpacing.xs) {
                                Image(systemName: "stethoscope")
                                    .font(.system(size: 16, weight: .semibold))
                                    .foregroundColor(.purple)

                                Text("System Diagnostics (Doctor)")
                                    .font(.headline)
                            }
                            Text("Real-time telemetry and subsystem integrity audit")
                                .font(.caption)
                                .foregroundColor(.secondary)
                        }

                        Spacer()

                        Button(action: {
                            store.runDoctorDiagnostics()
                        }) {
                            HStack(spacing: 4) {
                                Image(systemName: "arrow.clockwise")
                                    .rotationEffect(.degrees(store.isRunningDoctor ? 360 : 0))
                                    .animation(store.isRunningDoctor ? Animation.linear(duration: 0.8).repeatForever(autoreverses: false) : .default, value: store.isRunningDoctor)
                                Text("Run Diagnostics")
                            }
                        }
                        .buttonStyle(EmilBouncyButtonStyle())
                    }

                    VStack(spacing: EmilSpacing.xs) {
                        ForEach(store.doctorResults) { check in
                            HStack(alignment: .top, spacing: EmilSpacing.sm) {
                                // Status Pill
                                Text(check.status.rawValue)
                                    .font(.system(size: 10, weight: .bold, design: .monospaced))
                                    .padding(.horizontal, 6)
                                    .padding(.vertical, 2)
                                    .background(check.status.color.opacity(0.14))
                                    .foregroundColor(check.status.color)
                                    .overlay(
                                        RoundedRectangle(cornerRadius: 4, style: .continuous)
                                            .stroke(check.status.color.opacity(0.3), lineWidth: 1)
                                    )
                                    .cornerRadius(4)
                                    .frame(width: 44, alignment: .center)

                                VStack(alignment: .leading, spacing: 2) {
                                    HStack {
                                        Text(check.name)
                                            .font(.subheadline)
                                            .fontWeight(.semibold)

                                        Spacer()

                                        Text(check.measurement)
                                            .font(.system(.caption2, design: .monospaced))
                                            .foregroundColor(.secondary)
                                    }

                                    Text(check.message)
                                        .font(.caption)
                                        .foregroundColor(.secondary)
                                }
                            }
                            .padding(.vertical, 4)

                            if check.id != store.doctorResults.last?.id {
                                Divider()
                                    .overlay(Color.primary.opacity(0.04))
                            }
                        }
                    }
                    .padding(EmilSpacing.sm)
                    .background(Color.primary.opacity(0.02))
                    .cornerRadius(8)
                }
                .padding(EmilSpacing.md)
                .emilCard()

                // MARK: - 3. Hardware Resources
                VStack(alignment: .leading, spacing: EmilSpacing.sm) {
                    Text("Hardware Resources")
                        .font(.headline)

                    HStack {
                        Text("CPU Allocation:")
                            .frame(width: 150, alignment: .leading)
                            .font(.subheadline)
                        Stepper("\(maxCPUs) vCPUs", value: $maxCPUs, in: 1...16)
                            .font(.system(.subheadline, design: .monospaced))
                    }

                    HStack {
                        Text("Memory Ceiling:")
                            .frame(width: 150, alignment: .leading)
                            .font(.subheadline)
                        Stepper("\(maxMemoryMB / 1024) GB (\(maxMemoryMB) MB)", value: $maxMemoryMB, in: 1024...32768, step: 1024)
                            .font(.system(.subheadline, design: .monospaced))
                    }

                    HStack {
                        Text("Virtual Disk Size:")
                            .frame(width: 150, alignment: .leading)
                            .font(.subheadline)
                        Stepper("\(maxDiskGB) GB", value: $maxDiskGB, in: 16...512, step: 16)
                            .font(.system(.subheadline, design: .monospaced))
                    }
                }
                .padding(EmilSpacing.md)
                .emilCard()

                // MARK: - 4. Engine Optimizations
                VStack(alignment: .leading, spacing: EmilSpacing.sm) {
                    Text("Engine Optimizations")
                        .font(.headline)

                    Toggle("Rosetta 2 x86 Emulation for Linux", isOn: $rosettaX86)
                        .help("Enable high-performance x86_64 translation on Apple Silicon")

                    Toggle("Fast VirtioFS with FSEvents Sync (< 1ms)", isOn: $fastVirtioFS)
                        .help("Eliminates file polling for file watchers (vite, webpack, next.js)")

                    Toggle("Aggressive Battery Saver", isOn: $batterySaver)
                        .help("Drops memory balloon immediately to 48MB when running on battery")

                    Toggle("Auto-start VM Daemon on macOS Login", isOn: $autoStart)
                }
                .padding(EmilSpacing.md)
                .emilCard()

                // MARK: - 5. Storage & Networking
                VStack(alignment: .leading, spacing: EmilSpacing.sm) {
                    Text("Storage & Networking")
                        .font(.headline)

                    HStack {
                        Text("Local DNS:")
                            .frame(width: 140, alignment: .leading)
                            .font(.subheadline)
                        TextField("Domain", text: $dnsDomain)
                            .textFieldStyle(.roundedBorder)
                            .font(.system(.body, design: .monospaced))
                    }

                    HStack {
                        Text("Storage Path:")
                            .frame(width: 140, alignment: .leading)
                            .font(.subheadline)
                        TextField("Path", text: $storageLocation)
                            .textFieldStyle(.roundedBorder)
                            .font(.system(.body, design: .monospaced))
                    }
                }
                .padding(EmilSpacing.md)
                .emilCard()

                // Save Actions
                HStack {
                    if saveSuccess {
                        Text("Settings saved successfully")
                            .font(.caption)
                            .foregroundColor(.green)
                            .transition(.opacity)
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
                        withAnimation(.emilSnappy) {
                            saveSuccess = true
                        }
                        DispatchQueue.main.asyncAfter(deadline: .now() + 2.5) {
                            withAnimation(.emilSnappy) {
                                saveSuccess = false
                            }
                        }
                    }
                    .buttonStyle(EmilProminentButtonStyle())
                }
                .padding(.top, EmilSpacing.xxs)
            }
            .padding(EmilSpacing.md)
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
