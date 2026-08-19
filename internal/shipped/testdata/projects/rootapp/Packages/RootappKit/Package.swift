// swift-tools-version: 6.0
//
// A LOCAL module of the app above, not a component in its own right. Detection
// only treats a Package.swift as a Swift stack when the repository has no
// .xcodeproj anywhere — see internal/detect/detect.go. Six of the seven Swift
// repositories in this fleet carry a package like this one, and every one would
// be a false positive.
//
// There is deliberately no Package.resolved beside it: without one Dependabot's
// swift ecosystem cannot read the package, so the rendered .github/dependabot.yml
// must contain no swift entry at all. An entry pointing at an unreadable
// manifest does not no-op, it aborts the whole Dependabot run daily.
import PackageDescription

let package = Package(
    name: "RootappKit",
    platforms: [.iOS(.v18)],
    products: [.library(name: "RootappKit", targets: ["RootappKit"])],
    targets: [.target(name: "RootappKit")]
)
