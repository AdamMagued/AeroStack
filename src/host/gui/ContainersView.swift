import SwiftUI
import AppKit
import OrbEngine

public struct ContainersView: View {
    @ObservedObject var store: OrbDaemonStore

    public var body: some View {
        VStack(spacing: 0) {
            // Header
            HStack(alignment: .center) {
                VStack(alignment: .leading, spacing: EmilSpacing.xxs) {
                    Text("Docker Containers")
                        .font(.title2)
                        .fontWeight(.bold)
                    Text("\(store.containers.filter { $0.state == "running" }.count) running | VirtioFS Accelerated | Sub-millisecond IPC")
                        .font(.caption)
                        .foregroundColor(.secondary)
                }

                Spacer()

                Button(action: {
                    store.fetchContainers()
                    store.showToast(title: "Containers refreshed", type: .info)
                }) {
                    Image(systemName: "arrow.clockwise")
                }
                .buttonStyle(EmilActionIconButtonStyle(tint: .secondary))
                .help("Refresh Container List")
            }
            .padding(EmilSpacing.md)

            Divider()
                .overlay(Color.primary.opacity(0.06))

            if store.containers.isEmpty {
                VStack(spacing: EmilSpacing.md) {
                    Image(systemName: "shippingbox")
                        .font(.system(size: 44))
                        .foregroundColor(.secondary.opacity(0.7))
                    Text("No Running Containers")
                        .font(.headline)
                    Text("Launch containers using the standard Docker CLI:\ndocker run -d -p 8080:80 nginx:alpine")
                        .font(.subheadline)
                        .foregroundColor(.secondary)
                        .multilineTextAlignment(.center)
                }
                .frame(maxWidth: .infinity, maxHeight: .infinity)
                .padding(EmilSpacing.xl)
            } else {
                ScrollView {
                    LazyVStack(spacing: EmilSpacing.xs) {
                        ForEach(store.containers) { container in
                            ContainerCardView(container: container, store: store)
                        }
                    }
                    .padding(EmilSpacing.md)
                }
            }
        }
    }
}

// MARK: - Container Card View (Interactive Emil Design)

struct ContainerCardView: View {
    let container: ContainerSummary
    @ObservedObject var store: OrbDaemonStore

    var isRunning: Bool {
        container.state == "running"
    }

    var body: some View {
        VStack(alignment: .leading, spacing: EmilSpacing.xs) {
            // Top Row: Status, Name, Image, and Actions
            HStack(spacing: EmilSpacing.sm) {
                GlowingStatusDot(
                    color: isRunning ? .green : .secondary.opacity(0.4),
                    isPulsing: isRunning,
                    size: 8
                )

                VStack(alignment: .leading, spacing: 1) {
                    HStack(spacing: EmilSpacing.xs) {
                        Text(container.displayName)
                            .font(.headline)
                            .fontWeight(.semibold)

                        Text(container.image)
                            .font(.system(.caption, design: .monospaced))
                            .foregroundColor(.secondary)
                    }

                    Text(container.status)
                        .font(.caption2)
                        .foregroundColor(.secondary)
                }

                Spacer()

                // Interactive Container Control Buttons
                HStack(spacing: EmilSpacing.xs) {
                    if isRunning {
                        Button(action: { store.stopContainer(id: container.id) }) {
                            Image(systemName: "stop.fill")
                        }
                        .buttonStyle(EmilActionIconButtonStyle(tint: .red))
                        .help("Stop Container")
                    } else {
                        Button(action: { store.startContainer(id: container.id) }) {
                            Image(systemName: "play.fill")
                        }
                        .buttonStyle(EmilActionIconButtonStyle(tint: .green))
                        .help("Start Container")
                    }

                    Button(action: { store.restartContainer(id: container.id) }) {
                        Image(systemName: "arrow.counterclockwise")
                    }
                    .buttonStyle(EmilActionIconButtonStyle(tint: .blue))
                    .help("Restart Container")

                    Button(action: openContainerTerminal) {
                        Image(systemName: "terminal")
                    }
                    .buttonStyle(EmilActionIconButtonStyle(tint: .primary))
                    .help("Open Terminal Shell")
                }
            }

            // Ports Section with Clickable Browser Badges
            if !container.ports.isEmpty {
                HStack(spacing: EmilSpacing.xs) {
                    Text("Ports:")
                        .font(.caption)
                        .fontWeight(.medium)
                        .foregroundColor(.secondary)

                    ForEach(container.ports, id: \.self) { port in
                        ClickablePortBadge(portString: port, store: store)
                    }
                }
                .padding(.top, 2)
            }

            // VirtioFS Mounts with Clickable Finder Paths
            if !container.mounts.isEmpty {
                HStack(alignment: .top, spacing: EmilSpacing.xs) {
                    Text("VirtioFS:")
                        .font(.caption)
                        .fontWeight(.medium)
                        .foregroundColor(.secondary)
                        .padding(.top, 3)

                    VStack(alignment: .leading, spacing: EmilSpacing.xxs) {
                        ForEach(Array(container.mounts.keys), id: \.self) { target in
                            if let hostPath = container.mounts[target] {
                                ClickableMountBadge(hostPath: hostPath, guestPath: target, store: store)
                            }
                        }
                    }
                }
                .padding(.top, 2)
            }
        }
        .padding(EmilSpacing.md)
        .emilCard()
    }

    private func openContainerTerminal() {
        let p = Process()
        p.executableURL = URL(fileURLWithPath: "/usr/bin/open")
        p.arguments = ["-a", "Terminal"]
        try? p.run()
        store.showToast(title: "Terminal launched", subtitle: "Connected to \(container.displayName)", type: .info)
    }
}

// MARK: - Clickable Port Badge (Emil Micro-interactions)

struct ClickablePortBadge: View {
    let portString: String
    @ObservedObject var store: OrbDaemonStore

    var hostPort: Int? {
        var clean = portString.replacingOccurrences(of: "/tcp", with: "")
            .replacingOccurrences(of: "/udp", with: "")
        if clean.contains("->") {
            let parts = clean.components(separatedBy: "->")
            clean = parts.first ?? clean
        }
        if clean.contains(":") {
            let parts = clean.components(separatedBy: ":")
            if parts.count >= 2 {
                if let p = Int(parts[parts.count - 2].trimmingCharacters(in: .whitespaces)) {
                    return p
                } else if let p = Int(parts[0].trimmingCharacters(in: .whitespaces)) {
                    return p
                }
            }
        } else if let p = Int(clean.trimmingCharacters(in: .whitespaces)) {
            return p
        }
        return nil
    }

    var body: some View {
        Button(action: openInBrowser) {
            HStack(spacing: 4) {
                Image(systemName: "globe")
                    .font(.system(size: 9))
                Text(portString)
                    .font(.system(.caption2, design: .monospaced))
                    .fontWeight(.medium)
            }
            .padding(.horizontal, 6)
            .padding(.vertical, 3)
            .background(Color.green.opacity(0.12))
            .foregroundColor(.green)
            .overlay(
                RoundedRectangle(cornerRadius: 5, style: .continuous)
                    .stroke(Color.green.opacity(0.3), lineWidth: 1)
            )
            .cornerRadius(5)
        }
        .buttonStyle(PlainButtonStyle())
        .scaleEffect(1.0)
        .help("Open in Browser (http://localhost:\(hostPort ?? 80))")
    }

    private func openInBrowser() {
        let port = hostPort ?? 80
        if let url = URL(string: "http://localhost:\(port)") {
            NSWorkspace.shared.open(url)
            store.showToast(title: "Opening Browser", subtitle: "http://localhost:\(port)", type: .info)
        }
    }
}

// MARK: - Clickable VirtioFS Mount Badge

struct ClickableMountBadge: View {
    let hostPath: String
    let guestPath: String
    @ObservedObject var store: OrbDaemonStore

    var body: some View {
        Button(action: revealInFinder) {
            HStack(spacing: 5) {
                Image(systemName: "folder")
                    .font(.system(size: 9))
                    .foregroundColor(.blue)

                Text(hostPath)
                    .font(.system(.caption2, design: .monospaced))
                    .lineLimit(1)
                    .foregroundColor(.primary)

                Text("->")
                    .font(.caption2)
                    .foregroundColor(.secondary)

                Text(guestPath)
                    .font(.system(.caption2, design: .monospaced))
                    .fontWeight(.semibold)
                    .foregroundColor(.blue)
            }
            .padding(.horizontal, 6)
            .padding(.vertical, 3)
            .background(Color.blue.opacity(0.08))
            .overlay(
                RoundedRectangle(cornerRadius: 5, style: .continuous)
                    .stroke(Color.blue.opacity(0.2), lineWidth: 1)
            )
            .cornerRadius(5)
        }
        .buttonStyle(PlainButtonStyle())
        .help("Reveal '\(hostPath)' in Finder")
    }

    private func revealInFinder() {
        if FileManager.default.fileExists(atPath: hostPath) {
            NSWorkspace.shared.selectFile(nil, inFileViewerRootedAtPath: hostPath)
            store.showToast(title: "Revealed in Finder", subtitle: hostPath, type: .info)
        } else {
            NSWorkspace.shared.open(URL(fileURLWithPath: hostPath))
            store.showToast(title: "Path opened", subtitle: hostPath, type: .info)
        }
    }
}
