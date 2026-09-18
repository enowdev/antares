# Antares macOS

This task ships the macOS app shell as a SwiftPM executable package:

```sh
swift build
```

No `.xcodeproj` is checked in yet because `xcodegen` was not available in this environment. Task 7 should add the `make macos` / `xcodebuild` entry point and generate or check in a project file.
