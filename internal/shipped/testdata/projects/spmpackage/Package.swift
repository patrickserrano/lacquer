// swift-tools-version: 6.0
import PackageDescription

let package = Package(
    name: "Spmkit",
    platforms: [.iOS(.v18), .macOS(.v15)],
    products: [.library(name: "Spmkit", targets: ["Spmkit"])],
    targets: [
        .target(name: "Spmkit"),
        .testTarget(name: "SpmkitTests", dependencies: ["Spmkit"]),
    ]
)
