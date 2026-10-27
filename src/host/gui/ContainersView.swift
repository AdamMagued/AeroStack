import SwiftUI
import OrbEngine

public struct ContainersView: View {
    @ObservedObject var store: OrbDaemonStore

    public var body: some View {
        VStack(spacing: 0) {
            // Header
            HStack {
                VStack(alignment: .leading, spacing: 2) {
                    Text("Docker Containers")
                        .font(.title2)
                        .fontWeight(.bold)
                    Text("\(store.containers.filter { $0.state == "running" }.count) running • VirtioFS Accelerated")
                        .font(.caption)
                        .foregroundColor(.secondary)
                }

                Spacer()

                Button(action: { store.fetchContainers() }) {
                    Image(systemName: "arrow.clockwise")
                }
                .help("Refresh Container List")
            }
            .padding()

            Divider()

            if store.containers.isEmpty {
                VStack(spacing: 16) {
                    Image(systemName: "shippingbox")
                        .font(.system(size: 48))
                        .foregroundColor(.secondary)
                    Text("No Running Containers")
                        .font(.headline)
                    Text("Start containers using standard Docker CLI: `docker run -it alpine sh`")
                        .font(.subheadline)
                        .foregroundColor(.secondary)
                }
                .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else {
                List(store.containers) { c in
                    VStack(alignment: .leading, spacing: 8) {
                        HStack(spacing: 12) {
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

                        if !c.ports.isEmpty {
                            HStack(spacing: 6) {
                                Text("Ports:")
                                    .font(.caption)
                                    .fontWeight(.medium)
                                ForEach(c.ports, id: \.self) { p in
                                    Text(p)
                                        .font(.system(.caption2, design: .monospaced))
                                        .padding(.horizontal, 6)
                                        .padding(.vertical, 2)
                                        .background(Color.green.opacity(0.12))
                                        .foregroundColor(.green)
                                        .cornerRadius(4)
                                }
                            }
                        }

                        if !c.mounts.isEmpty {
                            HStack(alignment: .top, spacing: 6) {
                                Text("VirtioFS:")
                                    .font(.caption)
                                    .fontWeight(.medium)
                                    .padding(.top, 2)

                                VStack(alignment: .leading, spacing: 4) {
                                    ForEach(Array(c.mounts.keys), id: \.self) { target in
                                        HStack(spacing: 4) {
                                            Text(c.mounts[target] ?? "")
                                                .font(.system(.caption2, design: .monospaced))
                                                .lineLimit(1)
                                            Text("➔")
                                                .font(.caption2)
                                                .foregroundColor(.secondary)
                                            Text(target)
                                                .font(.system(.caption2, design: .monospaced))
                                                .fontWeight(.semibold)
                                        }
                                        .padding(.horizontal, 6)
                                        .padding(.vertical, 2)
                                        .background(Color.blue.opacity(0.1))
                                        .cornerRadius(4)
                                    }
                                }
                            }
                        }
                    }
                    .padding(.vertical, 6)
                }
            }
        }
    }
}
