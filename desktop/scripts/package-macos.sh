#!/usr/bin/env bash
# Wrap a built Antares.app in a compressed disk image with an Applications
# link, so it installs by dragging.
#
#   scripts/package-macos.sh <Antares.app> <out.dmg>
set -euo pipefail

app="$1"
out="$2"
[[ -d "$app" ]] || { echo "package-macos.sh: $app is not a directory" >&2; exit 1; }

stage="$(mktemp -d)"
trap 'rm -rf "$stage"' EXIT
cp -R "$app" "$stage/Antares.app"
ln -s /Applications "$stage/Applications"

rm -f "$out"
mkdir -p "$(dirname "$out")"
hdiutil create -quiet -volname Antares -srcfolder "$stage" -fs HFS+ -format UDZO -ov "$out"
echo "packaged $out"
