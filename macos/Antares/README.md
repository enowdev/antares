# Antares macOS

Native SwiftUI dashboard. Build the app bundle (Swift executable + bundled `antares` backend):

```sh
make macos
```

The result is `macos/build/Build/Products/Release/Antares.app`.

Package-level checks:

```sh
swift test --package-path macos/AntaresKit
swift test --package-path macos/Antares
swift build --package-path macos/Antares
```

Override the loopback API with `ANTARES_URL` (shared by the HTTP client and the runtime supervisor).
