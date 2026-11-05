#!/usr/bin/env bash
# Build Antares release artifacts for every supported platform, locally.
#
# .github/workflows/release.yml runs this on a tag push and publishes the
# result with the desktop packages. Run it locally to test a build.
#
# It builds the dashboard once, embeds it, then cross-compiles the binary for
# each target. Output goes to dist/release/ as:
#   antares_<version>_<os>_<arch>[.exe]   the binary
#   checksums.txt                          sha256 of every artifact
#
# Usage:
#   ./scripts/release-build.sh                 # version from git describe
#   VERSION=v0.1.0 ./scripts/release-build.sh  # explicit version
#
# Then publish (needs the gh CLI, authenticated):
#   gh release create v0.1.0 dist/release/* --title v0.1.0 --notes "…"
#   # or upload to an existing release:
#   gh release upload v0.1.0 dist/release/*

set -euo pipefail

cd "$(dirname "$0")/.."

GO="${GO:-go}"
BUN="${BUN:-bun}"
OUT="dist/release"

VERSION="${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo 0.1.0-dev)}"
COMMIT="$(git rev-parse --short HEAD 2>/dev/null || echo dev)"
DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
LDFLAGS="-s -w \
  -X github.com/enowdev/antares/internal/version.Version=$VERSION \
  -X github.com/enowdev/antares/internal/version.Commit=$COMMIT \
  -X github.com/enowdev/antares/internal/version.Date=$DATE"

# os/arch pairs to ship. GOOS GOARCH.
TARGETS=(
  "linux amd64"
  "linux arm64"
  "darwin amd64"
  "darwin arm64"
  "windows amd64"
  "windows arm64"
)

info() { printf '\033[36m==>\033[0m %s\n' "$*"; }

info "version $VERSION ($COMMIT)"

# Build the dashboard once; it is the same bytes embedded into every binary.
info "building the dashboard"
( cd web && $BUN install && $BUN run build )
rm -rf internal/server/dist
cp -r web/dist internal/server/dist
touch internal/server/dist/.gitkeep

rm -rf "$OUT"
mkdir -p "$OUT"

for t in "${TARGETS[@]}"; do
  read -r goos goarch <<<"$t"
  ext=""
  [ "$goos" = "windows" ] && ext=".exe"
  name="antares_${VERSION}_${goos}_${goarch}${ext}"
  info "building $goos/$goarch → $name"
  CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" \
    $GO build -trimpath -ldflags "$LDFLAGS" -o "$OUT/$name" ./cmd/antares
done

info "writing checksums"
( cd "$OUT" && shasum -a 256 antares_* > checksums.txt 2>/dev/null || sha256sum antares_* > checksums.txt )

info "done — artifacts in $OUT:"
ls -1 "$OUT"
echo
info "publish with: gh release create $VERSION $OUT/* --title $VERSION"
