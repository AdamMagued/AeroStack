import SwiftUI
import AppKit

@main
struct OrbStackFreeApp: App {
    var body: some Scene {
        WindowGroup("OrbStack Free", id: "main") {
            OrbMainView()
        }
        .windowStyle(.titleBar)
        .windowToolbarStyle(.unified)
        .defaultSize(width: 920, height: 620)
    }
}
