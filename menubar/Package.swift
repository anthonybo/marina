// swift-tools-version:6.0
import PackageDescription

let package = Package(
    name: "MarinaMenu",
    platforms: [.macOS(.v13)],
    targets: [
        .executableTarget(
            name: "MarinaMenu",
            path: "Sources/Marina",
            // The v5 language mode keeps this small AppKit agent free of
            // strict-concurrency ceremony it gains nothing from.
            swiftSettings: [.swiftLanguageMode(.v5)]
        )
    ]
)
