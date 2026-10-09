// swift-tools-version: 6.0
import PackageDescription

let package = Package(
    name: "alpha",
    platforms: [.macOS(.v15)],
    targets: [.executableTarget(name: "alpha")]
)
