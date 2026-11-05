// swift-tools-version: 6.0
import PackageDescription

let package = Package(
    name: "AntaresKit",
    platforms: [.macOS(.v14)],
    products: [.library(name: "AntaresKit", targets: ["AntaresKit"])],
    targets: [
        .target(name: "AntaresKit"),
        .testTarget(name: "AntaresKitTests", dependencies: ["AntaresKit"]),
    ]
)
