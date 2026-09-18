// swift-tools-version: 6.0
import PackageDescription

let package = Package(
    name: "Antares",
    platforms: [.macOS(.v14)],
    products: [
        .executable(name: "Antares", targets: ["Antares"]),
    ],
    dependencies: [
        .package(path: "../AntaresKit"),
    ],
    targets: [
        .executableTarget(
            name: "Antares",
            dependencies: ["AntaresKit"],
            path: "Antares"
        ),
        .testTarget(
            name: "AntaresTests",
            dependencies: ["Antares"],
            path: "Tests/AntaresTests"
        ),
    ]
)
