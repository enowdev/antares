#!/usr/bin/env bash
# Build desktop/bin/Antares.app: the Wails shell plus a copy of the antares
# binary in Contents/Resources, ad-hoc signed so it runs locally.
#
#   ANTARES_BIN   antares binary to bundle (default ../bin/antares)
#   VERSION       version string for the shell and Info.plist (default 0.1.0)
#   GO            go command (default: go on PATH)
set -euo pipefail

here="$(cd "$(dirname "$0")/.." && pwd)"
cd "$here"

GO="${GO:-go}"
VERSION="${VERSION:-0.1.0}"
ANTARES_BIN="${ANTARES_BIN:-$here/../bin/antares}"
APP="$here/bin/Antares.app"

if [[ "$(uname -s)" != "Darwin" ]]; then
  echo "build-macos.sh: macOS only" >&2
  exit 1
fi
if [[ ! -x "$ANTARES_BIN" ]]; then
  echo "build-macos.sh: $ANTARES_BIN not found — run 'make build' at the repo root first" >&2
  exit 1
fi

# Plist versions must be numeric (1.2.3); keep the leading numbers of a git
# describe like v0.5.0-16-g9b24008.
plist_version="$(printf '%s' "${VERSION#v}" | sed -E 's/^([0-9]+(\.[0-9]+){0,2}).*/\1/')"
[[ "$plist_version" =~ ^[0-9] ]] || plist_version="0.1.0"

export CGO_ENABLED=1
export MACOSX_DEPLOYMENT_TARGET=12.0
export CGO_CFLAGS="-mmacosx-version-min=12.0"
export CGO_LDFLAGS="-mmacosx-version-min=12.0"

mkdir -p bin
"$GO" build -tags production -trimpath -buildvcs=false \
  -ldflags "-w -s -X main.Version=$VERSION" -o bin/Antares .

rm -rf "$APP"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"
cp bin/Antares "$APP/Contents/MacOS/Antares"
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
echo "built $APP ($VERSION)"
