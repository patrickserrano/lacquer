// swift-tools-version: 6.0
//
// A local module of the app. It sits under the app component, so the Xcode
// project builds it in Test and the Lint job's package build leaves it alone.
import PackageDescription

let package = Package(
    name: "MultiswiftCore",
    platforms: [.iOS(.v18)],
    products: [.library(name: "MultiswiftCore", targets: ["MultiswiftCore"])],
    targets: [.target(name: "MultiswiftCore")]
)
