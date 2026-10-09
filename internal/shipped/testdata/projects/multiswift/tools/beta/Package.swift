// swift-tools-version: 6.0
import PackageDescription

let package = Package(
    name: "Beta",
    platforms: [.macOS(.v15)],
    targets: [
        .target(name: "Beta"),
        .testTarget(name: "BetaTests", dependencies: ["Beta"]),
    ]
)
