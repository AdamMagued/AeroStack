// swift-tools-version: 5.9
import PackageDescription

let package = Package(
    name: "AeroStack",
    platforms: [
        .macOS(.v13)
    ],
    products: [
        .executable(name: "aero-vmd", targets: ["aero-vmd"]),
        .executable(name: "aero-gui", targets: ["aero-gui"]),
        .executable(name: "orb-vmd", targets: ["aero-vmd"]),
        .executable(name: "orb-gui", targets: ["aero-gui"]),
        .library(name: "OrbEngine", targets: ["OrbEngine"]),
    ],
    targets: [
        .target(
            name: "OrbEngine",
            path: "src/host/engine"
        ),
        .executableTarget(
            name: "aero-vmd",
            dependencies: ["OrbEngine"],
            path: "src/host/vmd"
        ),
        .executableTarget(
            name: "aero-gui",
            dependencies: ["OrbEngine"],
            path: "src/host/gui"
        ),
        .testTarget(
            name: "OrbEngineTests",
            dependencies: ["OrbEngine"],
            path: "tests/engine"
        ),
    ]
)
