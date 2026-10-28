import SwiftUI
import AppKit
import OrbEngine

public struct MachinesView: View {
    @ObservedObject var store: OrbDaemonStore
    @Binding var showCreateSheet: Bool
    @State private var filterRunningOnly: Bool = false
    @State private var cloneSourceMachine: String? = nil
    @State private var machineToDelete: VMSummary? = nil

    public var filteredMachines: [VMSummary] {
        if filterRunningOnly {
            return store.machines.filter { $0.isRunning }
        }
        return store.machines
    }

    public var body: some View {
        VStack(spacing: 0) {
            // Header Bar
            HStack(alignment: .center) {
                VStack(alignment: .leading, spacing: EmilSpacing.xxs) {
                    Text("Linux MicroVMs")
                        .font(.title2)
                        .fontWeight(.bold)
                    Text("\(store.machines.filter(\.isRunning).count) running | \(store.machines.count) total machines | Sub-second boot")
                        .font(.caption)
                        .foregroundColor(.secondary)
                }

                Spacer()

                Toggle("Running Only", isOn: $filterRunningOnly)
                    .toggleStyle(.checkbox)
                    .padding(.trailing, EmilSpacing.xs)

                Button(action: { showCreateSheet = true }) {
                    Label("New Machine", systemImage: "plus")
                }
                .buttonStyle(EmilProminentButtonStyle(customColor: .accentColor))
            }
            .padding(EmilSpacing.md)

            Divider()
                .overlay(Color.primary.opacity(0.06))

            if filteredMachines.isEmpty {
                VStack(spacing: EmilSpacing.md) {
                    Image(systemName: "desktopcomputer.trianglebadge.exclamationmark")
                        .font(.system(size: 44))
                        .foregroundColor(.secondary.opacity(0.7))
                    Text("No Linux Machines Found")
                        .font(.headline)
                    Text("Instant snapshot microVMs with sub-second boot time and dynamic ballooning.")
                        .font(.subheadline)
                        .foregroundColor(.secondary)
                        .multilineTextAlignment(.center)
                    Button("Create Machine") {
                        showCreateSheet = true
                    }
                    .buttonStyle(EmilProminentButtonStyle())
                }
                .frame(maxWidth: .infinity, maxHeight: .infinity)
                .padding(EmilSpacing.xl)
            } else {
                ScrollView {
                    LazyVStack(spacing: EmilSpacing.xs) {
                        ForEach(filteredMachines) { machine in
                            MachineCardView(
                                machine: machine,
                                store: store,
                                onClone: { cloneSourceMachine = machine.name },
                                onDelete: { machineToDelete = machine }
                            )
                        }
                    }
                    .padding(EmilSpacing.md)
                }
            }
        }
        .sheet(item: Binding<CloneSheetPayload?>(
            get: { cloneSourceMachine.map { CloneSheetPayload(sourceName: $0) } },
            set: { cloneSourceMachine = $0?.sourceName }
        )) { payload in
            CloneMachineSheet(sourceMachine: payload.sourceName, store: store)
        }
        .confirmationDialog(
            "Delete \(machineToDelete?.name ?? "Machine")?",
            isPresented: Binding<Bool>(
                get: { machineToDelete != nil },
                set: { if !$0 { machineToDelete = nil } }
            ),
            titleVisibility: .visible
        ) {
            Button("Delete Machine", role: .destructive) {
                if let m = machineToDelete {
                    store.deleteMachine(name: m.name)
                    machineToDelete = nil
                }
            }
            Button("Cancel", role: .cancel) {
                machineToDelete = nil
            }
        } message: {
            Text("This action permanently removes the microVM metadata and associated virtual disk snapshots.")
        }
    }
}

// MARK: - Helper Identifiable Payload for Sheets

struct CloneSheetPayload: Identifiable {
    let sourceName: String
    var id: String { sourceName }
}

// MARK: - Machine Card View (Emil Kowalski Physics & Craft)

struct MachineCardView: View {
    let machine: VMSummary
    @ObservedObject var store: OrbDaemonStore
    let onClone: () -> Void
    let onDelete: () -> Void

    var body: some View {
        HStack(spacing: EmilSpacing.md) {
            // Status Indicator with subtle glow
            GlowingStatusDot(
                color: machine.isRunning ? .green : .secondary.opacity(0.4),
                isPulsing: machine.isRunning,
                size: 8
            )

            // Info Column
            VStack(alignment: .leading, spacing: EmilSpacing.xxs) {
                HStack(spacing: EmilSpacing.xs) {
                    Text(machine.name)
                        .font(.system(.headline, design: .default))
                        .fontWeight(.semibold)

                    DistroBadgeView(distro: machine.distro)
                }

                Text("\(machine.version) | \(machine.arch) | \(machine.cpus) vCPUs | \(machine.memory_mb) MB RAM | \(machine.size)")
                    .font(.caption)
                    .foregroundColor(.secondary)
            }

            Spacer()

            // IP Address Badge
            if machine.isRunning {
                HStack(spacing: 4) {
                    Image(systemName: "network")
                        .font(.caption2)
                        .foregroundColor(.secondary)
                    Text(machine.ip_address)
                        .font(.system(.caption, design: .monospaced))
                        .foregroundColor(.primary)
                }
                .padding(.horizontal, EmilSpacing.xs)
                .padding(.vertical, 3)
                .background(Color.primary.opacity(0.04))
                .overlay(
                    RoundedRectangle(cornerRadius: 6, style: .continuous)
                        .stroke(Color.primary.opacity(0.08), lineWidth: 1)
                )
            }

            // Action Buttons
            HStack(spacing: EmilSpacing.xs) {
                if machine.isRunning {
                    Button(action: { store.stopMachine(name: machine.name) }) {
                        Image(systemName: "stop.fill")
                    }
                    .buttonStyle(EmilActionIconButtonStyle(tint: .red))
                    .help("Stop Machine")
                } else {
                    Button(action: { store.startMachine(name: machine.name) }) {
                        Image(systemName: "play.fill")
                    }
                    .buttonStyle(EmilActionIconButtonStyle(tint: .green))
                    .help("Start Machine")
                }

                Button(action: onClone) {
                    Image(systemName: "doc.on.doc")
                }
                .buttonStyle(EmilActionIconButtonStyle(tint: .blue))
                .help("Clone Machine (Instant CoW)")

                Button(action: {
                    openTerminalShell(machineName: machine.name)
                }) {
                    Image(systemName: "terminal")
                }
                .buttonStyle(EmilActionIconButtonStyle(tint: .primary))
                .help("Open Terminal Shell")

                Button(action: onDelete) {
                    Image(systemName: "trash")
                }
                .buttonStyle(EmilActionIconButtonStyle(tint: .secondary, hoverTint: .red))
                .help("Delete Machine")
            }
        }
        .padding(.horizontal, EmilSpacing.md)
        .padding(.vertical, EmilSpacing.sm)
        .emilCard()
    }

    private func openTerminalShell(machineName: String) {
        let p = Process()
        p.executableURL = URL(fileURLWithPath: "/usr/bin/open")
        p.arguments = ["-a", "Terminal"]
        try? p.run()
    }
}

// MARK: - Distro Badge

struct DistroBadgeView: View {
    let distro: String

    var tintColor: Color {
        switch distro.lowercased() {
        case "ubuntu": return Color.orange
        case "alpine": return Color.blue
        case "debian": return Color.red
        case "fedora": return Color.blue
        case "arch": return Color.cyan
        default: return Color.purple
        }
    }

    var body: some View {
        Text(distro)
            .font(.system(size: 11, weight: .medium))
            .padding(.horizontal, 6)
            .padding(.vertical, 2)
            .background(tintColor.opacity(0.12))
            .foregroundColor(tintColor)
            .overlay(
                RoundedRectangle(cornerRadius: 4, style: .continuous)
                    .stroke(tintColor.opacity(0.25), lineWidth: 1)
            )
            .cornerRadius(4)
    }
}

// MARK: - Clone Machine Sheet (APFS Instant CoW)

public struct CloneMachineSheet: View {
    public let sourceMachine: String
    @ObservedObject public var store: OrbDaemonStore
    @Environment(\.dismiss) private var dismiss

    @State private var targetName: String = ""

    public var body: some View {
        VStack(alignment: .leading, spacing: EmilSpacing.lg) {
            // Header
            VStack(alignment: .leading, spacing: EmilSpacing.xxs) {
                Text("Clone MicroVM")
                    .font(.title2)
                    .fontWeight(.bold)
                Text("Instant copy-on-write APFS snapshot with zero storage overhead.")
                    .font(.caption)
                    .foregroundColor(.secondary)
            }

            // Source Card
            HStack(spacing: EmilSpacing.sm) {
                Image(systemName: "desktopcomputer")
                    .font(.system(size: 16, weight: .semibold))
                    .foregroundColor(.blue)

                VStack(alignment: .leading, spacing: 2) {
                    Text("Source MicroVM")
                        .font(.caption2)
                        .foregroundColor(.secondary)
                    Text(sourceMachine)
                        .font(.headline)
                }
                Spacer()
                Text("CoW Ready")
                    .font(.system(.caption2, design: .monospaced))
                    .padding(.horizontal, 6)
                    .padding(.vertical, 2)
                    .background(Color.green.opacity(0.12))
                    .foregroundColor(.green)
                    .cornerRadius(4)
            }
            .padding(EmilSpacing.sm)
            .emilCard()

            // Destination Name Field
            VStack(alignment: .leading, spacing: EmilSpacing.xxs) {
                Text("Clone Destination Name")
                    .font(.subheadline)
                    .fontWeight(.medium)

                TextField("e.g. \(sourceMachine)-clone", text: $targetName)
                    .textFieldStyle(.roundedBorder)
                    .font(.system(.body, design: .monospaced))
            }

            // Footer Actions
            HStack {
                Spacer()

                Button("Cancel") {
                    dismiss()
                }
                .buttonStyle(EmilBouncyButtonStyle())
                .keyboardShortcut(.cancelAction)

                Button(action: performClone) {
                    Label("Instant CoW Clone", systemImage: "doc.on.doc.fill")
                }
                .buttonStyle(EmilProminentButtonStyle(customColor: .blue))
                .keyboardShortcut(.defaultAction)
            }
            .padding(.top, EmilSpacing.xs)
        }
        .padding(EmilSpacing.xl)
        .frame(width: 460)
        .onAppear {
            if targetName.isEmpty {
                targetName = "\(sourceMachine)-clone"
            }
        }
    }

    private func performClone() {
        let cleanName = targetName.trimmingCharacters(in: .whitespacesAndNewlines)
        let finalTarget = cleanName.isEmpty ? "\(sourceMachine)-clone" : cleanName
        store.cloneMachine(source: sourceMachine, destination: finalTarget)
        dismiss()
    }
}

// MARK: - Create Machine Sheet

public struct CreateMachineSheet: View {
    @ObservedObject var store: OrbDaemonStore
    @Environment(\.dismiss) private var dismiss

    @State private var machineName: String = ""
    @State private var selectedDistro: String = "Ubuntu"
    let distros = ["Ubuntu", "Alpine", "Debian", "Fedora", "Arch"]

    public var body: some View {
        VStack(alignment: .leading, spacing: EmilSpacing.lg) {
            VStack(alignment: .leading, spacing: EmilSpacing.xxs) {
                Text("Create Linux Machine")
                    .font(.title2)
                    .fontWeight(.bold)
                Text("Deploy an isolated microVM with sub-second initialization.")
                    .font(.caption)
                    .foregroundColor(.secondary)
            }

            VStack(alignment: .leading, spacing: EmilSpacing.xs) {
                Text("Distribution")
                    .font(.subheadline)
                    .fontWeight(.medium)

                Picker("", selection: $selectedDistro) {
                    ForEach(distros, id: \.self) { d in
                        Text(d).tag(d)
                    }
                }
                .pickerStyle(.segmented)
            }

            VStack(alignment: .leading, spacing: EmilSpacing.xs) {
                Text("Machine Name")
                    .font(.subheadline)
                    .fontWeight(.medium)

                TextField("e.g. dev-sandbox", text: $machineName)
                    .textFieldStyle(.roundedBorder)
                    .font(.system(.body, design: .monospaced))
            }

            HStack {
                Spacer()

                Button("Cancel") {
                    dismiss()
                }
                .buttonStyle(EmilBouncyButtonStyle())
                .keyboardShortcut(.cancelAction)

                Button("Create Machine") {
                    let clean = machineName.trimmingCharacters(in: .whitespacesAndNewlines)
                    let name = clean.isEmpty ? selectedDistro.lowercased() : clean
                    store.createMachine(name: name, distro: selectedDistro)
                    dismiss()
                }
                .buttonStyle(EmilProminentButtonStyle())
                .keyboardShortcut(.defaultAction)
            }
            .padding(.top, EmilSpacing.xs)
        }
        .padding(EmilSpacing.xl)
        .frame(width: 440)
    }
}
