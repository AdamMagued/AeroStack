import SwiftUI
import AppKit

@main
struct OrbStackFreeApp: App {
    @StateObject private var store = OrbDaemonStore.shared

    var body: some Scene {
        WindowGroup("AeroStack", id: "main") {
            OrbMainView(store: store)
        }
        .windowStyle(.titleBar)
        .windowToolbarStyle(.unified)
        .defaultSize(width: 920, height: 620)

        MenuBarExtra("AeroStack", systemImage: "bolt.fill") {
            VStack(alignment: .leading) {
                Text(store.isConnected ? "AeroStack: Running" : "AeroStack: Stopped")
                    .font(.headline)

                Divider()

                Button("Start AeroStack") {
                    store.ensureDaemonRunning()
                }
                .disabled(store.isConnected)

                Button("Stop AeroStack") {
                    store.stopMachine(name: "aerostack")
                }
                .disabled(!store.isConnected)

                Divider()

                Button("Open AeroStack") {
                    NSApp.activate(ignoringOtherApps: true)
                    for window in NSApp.windows where window.canBecomeMain {
                        window.makeKeyAndOrderFront(nil)
                    }
                }

                Divider()

                Button("Quit AeroStack") {
                    NSApplication.shared.terminate(nil)
                }
            }
        }
    }
}
