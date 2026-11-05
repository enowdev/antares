#!/usr/bin/env bash
# Build desktop/bin/Antares.app: the Wails shell plus a copy of the antares
# binary in Contents/Resources, ad-hoc signed so it runs locally.
#
#   ANTARES_BIN   antares binary to bundle (default ../bin/antares)
#   VERSION       version string for the shell and Info.plist (default 0.1.0)
#   GO            go command (default: go on PATH)
#   ARCH          arm64 or amd64 (default: this machine); cgo cross-builds
#                 the other one with clang's -arch
#   APP           where to write the bundle (default bin/Antares.app)
set -euo pipefail

here="$(cd "$(dirname "$0")/.." && pwd)"
cd "$here"

GO="${GO:-go}"
VERSION="${VERSION:-0.1.0}"
ANTARES_BIN="${ANTARES_BIN:-$here/../bin/antares}"
APP="${APP:-$here/bin/Antares.app}"
ARCH="${ARCH:-$("$GO" env GOARCH)}"
case "$ARCH" in arm64) clang_arch=arm64 ;; amd64) clang_arch=x86_64 ;; *) echo "build-macos.sh: ARCH must be arm64 or amd64" >&2; exit 1 ;; esac

if [[ "$(uname -s)" != "Darwin" ]]; then
  echo "build-macos.sh: macOS only" >&2
  exit 1
fi
if [[ ! -x "$ANTARES_BIN" ]]; then
  echo "build-macos.sh: $ANTARES_BIN not found - run 'make build' at the repo root first" >&2
  exit 1
fi

# Plist versions must be numeric (1.2.3); keep the leading numbers of a git
# describe like v0.5.0-16-g9b24008.
plist_version="$(printf '%s' "${VERSION#v}" | sed -E 's/^([0-9]+(\.[0-9]+){0,2}).*/\1/')"
[[ "$plist_version" =~ ^[0-9] ]] || plist_version="0.1.0"

export CGO_ENABLED=1
export MACOSX_DEPLOYMENT_TARGET=12.0
export GOOS=darwin GOARCH="$ARCH"
export CGO_CFLAGS="-mmacosx-version-min=12.0 -arch $clang_arch"
export CGO_LDFLAGS="-mmacosx-version-min=12.0 -arch $clang_arch"

mkdir -p bin
"$GO" build -tags production -trimpath -buildvcs=false \
  -ldflags "-w -s -X main.Version=$VERSION" -o "bin/Antares-$ARCH" .

rm -rf "$APP"
mkdir -p "$(dirname "$APP")" "$APP/Contents/MacOS" "$APP/Contents/Resources"
cp "bin/Antares-$ARCH" "$APP/Contents/MacOS/Antares"
cp build/darwin/icons.icns "$APP/Contents/Resources/icons.icns"
cp "$ANTARES_BIN" "$APP/Contents/Resources/antares"
chmod 755 "$APP/Contents/Resources/antares"
sed "s/@VERSION@/$plist_version/g" build/darwin/Info.plist > "$APP/Contents/Info.plist"
plutil -lint "$APP/Contents/Info.plist" >/dev/null

# Ad hoc: no identity yet (not notarized; Gatekeeper will ask on first open
# of a downloaded copy). Sign the nested binary first, then the bundle.
codesign --force --sign - --timestamp=none "$APP/Contents/Resources/antares"
codesign --force --sign - --timestamp=none "$APP"
codesign --verify --strict "$APP"
echo "built $APP ($VERSION, $ARCH)"
