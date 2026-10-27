import SwiftUI
import OrbEngine

public struct MachinesView: View {
    @ObservedObject var store: OrbDaemonStore
    @Binding var showCreateSheet: Bool
    @State private var filterRunningOnly: Bool = false

    public var filteredMachines: [VMSummary] {
        if filterRunningOnly {
            return store.machines.filter { $0.isRunning }
        }
        return store.machines
    }

    public var body: some View {
        VStack(spacing: 0) {
            // Header
            HStack {
                VStack(alignment: .leading, spacing: 2) {
                    Text("Linux MicroVMs")
                        .font(.title2)
                        .fontWeight(.bold)
                    Text("\(store.machines.filter(\.isRunning).count) running • \(store.machines.count) total machines")
                        .font(.caption)
                        .foregroundColor(.secondary)
                }

                Spacer()

                Toggle("Running Only", isOn: $filterRunningOnly)
                    .toggleStyle(.checkbox)
                    .padding(.trailing, 8)

                Button(action: { showCreateSheet = true }) {
                    Label("New Machine", systemImage: "plus")
                }
                .buttonStyle(.borderedProminent)
            }
            .padding()

            Divider()

            if filteredMachines.isEmpty {
                VStack(spacing: 16) {
                    Image(systemName: "desktopcomputer.trianglebadge.exclamationmark")
                        .font(.system(size: 48))
                        .foregroundColor(.secondary)
                    Text("No Linux Machines Found")
                        .font(.headline)
                    Text("Instant snapshot microVMs with sub-second boot time.")
                        .font(.subheadline)
                        .foregroundColor(.secondary)
                    Button("Create Machine") {
                        showCreateSheet = true
                    }
                    .buttonStyle(.borderedProminent)
                }
                .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else {
                List(filteredMachines) { machine in
                    MachineRowView(machine: machine, store: store)
                        .padding(.vertical, 4)
                }
            }
        }
    }
}

struct MachineRowView: View {
    let machine: VMSummary
    @ObservedObject var store: OrbDaemonStore

    var body: some View {
        HStack(spacing: 16) {
            Circle()
                .fill(machine.isRunning ? Color.green : Color.gray.opacity(0.4))
                .frame(width: 10, height: 10)

            VStack(alignment: .leading, spacing: 3) {
                HStack(spacing: 6) {
                    Text(machine.name)
                        .font(.headline)
                    Text(machine.distro)
                        .font(.caption2)
                        .padding(.horizontal, 6)
                        .padding(.vertical, 2)
                        .background(Color.blue.opacity(0.15))
                        .foregroundColor(.blue)
                        .cornerRadius(4)
                }

                Text("\(machine.version) • \(machine.arch) • \(machine.cpus) vCPUs • \(machine.memory_mb) MB RAM • \(machine.size)")
                    .font(.caption)
                    .foregroundColor(.secondary)
            }

            Spacer()

            if machine.isRunning {
                HStack(spacing: 4) {
                    Image(systemName: "network")
                        .font(.caption2)
                    Text(machine.ip_address)
                        .font(.system(.caption, design: .monospaced))
                }
                .padding(.horizontal, 6)
                .padding(.vertical, 3)
                .background(Color.secondary.opacity(0.1))
                .cornerRadius(4)

                Button(action: { store.stopMachine(name: machine.name) }) {
                    Image(systemName: "stop.fill")
                        .foregroundColor(.red)
                }
                .help("Stop Machine")
                .buttonStyle(.borderless)
            } else {
                Button(action: { store.startMachine(name: machine.name) }) {
                    Image(systemName: "play.fill")
                        .foregroundColor(.green)
                }
                .help("Start Machine")
                .buttonStyle(.borderless)
            }

            Button(action: {
                // Open Terminal shell
                let p = Process()
                p.executableURL = URL(fileURLWithPath: "/usr/bin/open")
                p.arguments = ["-a", "Terminal"]
                try? p.run()
            }) {
                Image(systemName: "terminal")
            }
            .help("Open Terminal Shell")
            .buttonStyle(.borderless)
        }
    }
}

public struct CreateMachineSheet: View {
    @ObservedObject var store: OrbDaemonStore
    @Environment(\.dismiss) private var dismiss

    @State private var machineName: String = ""
    @State private var selectedDistro: String = "Ubuntu"
    let distros = ["Ubuntu", "Alpine", "Debian", "Fedora", "Arch"]

    public var body: some View {
        VStack(alignment: .leading, spacing: 20) {
            Text("Create Linux Machine")
                .font(.title2)
                .fontWeight(.bold)

            VStack(alignment: .leading, spacing: 6) {
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

            VStack(alignment: .leading, spacing: 6) {
                Text("Machine Name")
                    .font(.subheadline)
                    .fontWeight(.medium)
                TextField("e.g. dev-sandbox", text: $machineName)
                    .textFieldStyle(.roundedBorder)
            }

            HStack {
                Spacer()
                Button("Cancel") {
                    dismiss()
                }
                .keyboardShortcut(.cancelAction)

                Button("Create") {
                    let name = machineName.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
                        ? selectedDistro.lowercased()
                        : machineName.trimmingCharacters(in: .whitespacesAndNewlines)
                    store.createMachine(name: name, distro: selectedDistro)
                    dismiss()
                }
                .buttonStyle(.borderedProminent)
                .keyboardShortcut(.defaultAction)
            }
            .padding(.top, 10)
        }
        .padding(24)
        .frame(width: 440)
    }
}
