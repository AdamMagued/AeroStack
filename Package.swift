// swift-tools-version: 5.9
import PackageDescription

let package = Package(
    name: "orbstack_free",
    platforms: [
        .macOS(.v13)
    ],
    products: [
        .executable(name: "orb-vmd", targets: ["orb-vmd"]),
        .executable(name: "orb-gui", targets: ["orb-gui"]),
        .library(name: "OrbEngine", targets: ["OrbEngine"]),
    ],
    targets: [
        .target(
            name: "OrbEngine",
            path: "src/host/engine"
        ),
        .executableTarget(
            name: "orb-vmd",
            dependencies: ["OrbEngine"],
            path: "src/host/vmd"
        ),
        .executableTarget(
            name: "orb-gui",
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
